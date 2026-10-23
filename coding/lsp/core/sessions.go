package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("sidekick/coding/lsp")

const defaultClientIdleTimeout = time.Minute
const defaultClientMaxAge = 5 * time.Minute

var defaultClientCapabilities = ClientCapabilities{
	TextDocument: TextDocumentClientCapabilities{
		Synchronization: &TextDocumentSyncClientCapabilities{
			DidSave: &[]bool{true}[0],
		},
		CodeAction: CodeActionClientCapabilities{
			CodeActionLiteralSupport: &CodeActionLiteralSupport{
				CodeActionKind: &CodeActionKindValueSet{
					ValueSet: []CodeActionKind{
						CodeActionKindSourceFixAll,
						CodeActionKindSourceOrganizeImports,
					},
				},
			},
		},
	},
}

// ClientScope identifies which cached client a request may reuse.
type ClientScope struct {
	BaseDir string
	// Identity distinguishes environments that share a BaseDir.
	Identity string
	// Configure prepares a newly provided client before initialization.
	Configure func(LSPClient)
}

// Sessions caches initialized language-server clients. A single gate is held
// for each entire operation, including compound document sync, which bounds
// live workspace graphs without terminating an in-flight request.
type Sessions struct {
	LSPClientProvider  func(language string) LSPClient
	InitializedClients map[string]LSPClient
	IdleTimeout        time.Duration
	MaxClientAge       time.Duration

	cacheOnce      sync.Once
	clientGate     chan struct{}
	idleTimer      *time.Timer
	idleGeneration uint64
	clientBorn     time.Time
	closed         bool
}

func (s *Sessions) gate() chan struct{} {
	s.cacheOnce.Do(func() {
		s.clientGate = make(chan struct{}, 1)
		s.clientGate <- struct{}{}
		if s.IdleTimeout == 0 {
			s.IdleTimeout = defaultClientIdleTimeout
		}
		if s.MaxClientAge == 0 {
			s.MaxClientAge = defaultClientMaxAge
		}
	})
	return s.clientGate
}

// Acquire returns an operation context and client for language within scope,
// initializing a client when none is cached, the cached one is too old, or
// its connection was lost. The returned release must be called exactly once.
func (s *Sessions) Acquire(ctx context.Context, scope ClientScope, lang string) (context.Context, LSPClient, func(), error) {
	_, span := tracer.Start(ctx, "findOrInitClient")
	defer span.End()
	span.SetAttributes(attribute.String("language", lang), attribute.String("baseDir", scope.BaseDir))

	if err := ctx.Err(); err != nil {
		return ctx, nil, nil, err
	}
	gate := s.gate()
	select {
	case <-ctx.Done():
		return ctx, nil, nil, ctx.Err()
	case <-gate:
	}
	succeeded := false
	defer func() {
		if !succeeded {
			gate <- struct{}{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return ctx, nil, nil, err
	}
	if s.closed {
		return ctx, nil, nil, fmt.Errorf("LSP activities are closed")
	}
	key := scope.BaseDir + ":" + lang + scope.Identity
	s.idleGeneration++
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	if s.InitializedClients == nil {
		s.InitializedClients = make(map[string]LSPClient)
	}
	if s.clientBorn.IsZero() {
		s.clientBorn = time.Now()
	}
	for range 2 {
		lspClient, ok := s.InitializedClients[key]
		if !ok || time.Since(s.clientBorn) >= s.MaxClientAge || !clientIsConnected(lspClient) {
			if err := s.closeClients(); err != nil {
				return ctx, nil, nil, fmt.Errorf("close previous analysis client: %w", err)
			}
			span.SetAttributes(attribute.Bool("initialized", true))
			lspClient = s.LSPClientProvider(lang)
			if scope.Configure != nil {
				scope.Configure(lspClient)
			}
			params := InitializeParams{
				ProcessID:    0,
				RootURI:      (&url.URL{Scheme: "file", Path: scope.BaseDir}).String(),
				Capabilities: defaultClientCapabilities,
			}
			if _, err := lspClient.Initialize(ctx, params); err != nil {
				err = fmt.Errorf("failed to initialize LSP client: %w", errors.Join(err, closeClient(lspClient)))
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return ctx, nil, nil, err
			}
			s.InitializedClients[key] = lspClient
			s.clientBorn = time.Now()
		} else {
			span.SetAttributes(attribute.Bool("cached", true))
		}
		operationCtx, finish, err := s.beginClientActivity(ctx, lspClient)
		if err != nil {
			return operationCtx, nil, nil, errors.Join(err, s.closeClients())
		}
		if !clientIsConnected(lspClient) {
			finish()
			if err := s.closeClients(); err != nil {
				return ctx, nil, nil, err
			}
			continue
		}
		succeeded = true
		return operationCtx, lspClient, func() {
			finish()
			s.releaseClient()
		}, nil
	}
	return ctx, nil, nil, fmt.Errorf("analysis connection disconnected during acquisition")
}

func closeClient(client LSPClient) error {
	if closer, ok := client.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Discard closes every cached client so the next acquisition starts fresh. It
// must only be called while holding a client returned by Acquire.
func (s *Sessions) Discard() error {
	return s.closeClients()
}

func (s *Sessions) closeClients() error {
	var result error
	for key, client := range s.InitializedClients {
		result = errors.Join(result, closeClient(client))
		delete(s.InitializedClients, key)
	}
	return result
}

func (s *Sessions) releaseClient() {
	generation := s.idleGeneration
	s.idleTimer = time.AfterFunc(s.IdleTimeout, func() {
		gate := s.gate()
		<-gate
		defer func() { gate <- struct{}{} }()
		if generation == s.idleGeneration {
			_ = s.closeClients()
		}
	})
	s.clientGate <- struct{}{}
}

func (s *Sessions) Close() error {
	gate := s.gate()
	<-gate
	defer func() { gate <- struct{}{} }()
	s.closed = true
	s.idleGeneration++
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	return s.closeClients()
}

// Clients that hold a remote activity lease expose BeginActivity; losing the
// lease cancels the operation and discards the client.
func (s *Sessions) beginClientActivity(ctx context.Context, client LSPClient) (context.Context, func(), error) {
	active, ok := client.(interface {
		BeginActivity(context.Context, func(error)) (func(), error)
	})
	if !ok {
		return ctx, func() {}, nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	var lost atomic.Bool
	end, err := active.BeginActivity(ctx, func(err error) {
		lost.Store(true)
		cancel(err)
		if err := closeClient(client); err != nil {
			log.Warn().Err(err).Msg("Failed to close analysis client after activity lease loss")
		}
	})
	if err != nil {
		cancel(err)
		return ctx, nil, err
	}
	release := func() {
		end()
		if lost.Load() {
			if err := s.closeClients(); err != nil {
				log.Warn().Err(err).Msg("Failed to discard analysis clients after activity lease loss")
			}
		}
		cancel(nil)
	}
	if err := ctx.Err(); err != nil {
		end()
		err = errors.Join(err, s.closeClients())
		cancel(nil)
		return ctx, nil, err
	}
	return ctx, release, nil
}

func clientIsConnected(client LSPClient) bool {
	if health, ok := client.(interface{ IsConnected() bool }); ok {
		return health.IsConnected()
	}
	return true
}