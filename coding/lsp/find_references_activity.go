package lsp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var lspTracer = otel.Tracer("sidekick/coding/lsp")

// NewLSPActivities creates a new LSPActivities instance with proper initialization
func NewLSPActivities(lspClientProvider func(lang string) LSPClient) *LSPActivities {
	return &LSPActivities{
		LSPClientProvider:  lspClientProvider,
		InitializedClients: make(map[string]LSPClient),
	}
}

// FindReferencesActivityInput represents the input for the FindReferencesActivity function
type FindReferencesActivityInput struct {
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolText       string
	Range            *Range // Optional

	// PreloadedFileContent, when non-nil, is used as RelativeFilePath's
	// content instead of reading it from the environment. It is intentionally
	// not serialized: activity invocations always read from the env.
	PreloadedFileContent []byte `json:"-"`
}

// FindReferencesActivity finds references for the given input using the LSP client.
// Returns ErrUnsupportedLanguage (wrapped) for languages without LSP support.
// Callers that want to silently skip unsupported languages should check with
// IsSupportedLanguage before calling, or use errors.Is on the returned error.
func (lspa *LSPActivities) FindReferencesActivity(ctx context.Context, input FindReferencesActivityInput) ([]Location, error) {
	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	lang := utils.InferLanguageNameFromFilePath(input.RelativeFilePath)
	lspClient, release, err := lspa.findOrInitClient(ctx, baseDir, lang, &input.EnvContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to find or initialize lsp client: %w", err)
	}
	defer release()

	fileBytes := input.PreloadedFileContent
	if fileBytes == nil {
		fileBytes, err = input.EnvContainer.Env.ReadFile(ctx, input.RelativeFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
	}
	reader := bufio.NewReader(bytes.NewReader(fileBytes))

	position, err := findSymbolPosition(reader, input.Range, input.SymbolText)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol position: %w", err)
	}

	absoluteFilepath := filepath.Join(baseDir, input.RelativeFilePath)
	uri, err := url.Parse("file://" + absoluteFilepath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse uri file://%s: %w", absoluteFilepath, err)
	}
	references, err := lspClient.TextDocumentReferences(ctx, uri.String(), position.Line, position.Character)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke lsp text document references: %w", err)
	}

	return references, nil
}

func (lspa *LSPActivities) findOrInitClient(ctx context.Context, baseDir string, lang string, envContainer *env.EnvContainer) (LSPClient, func(), error) {
	_, span := lspTracer.Start(ctx, "findOrInitClient")
	defer span.End()
	span.SetAttributes(attribute.String("language", lang), attribute.String("baseDir", baseDir))

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	gate := lspa.gate()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-gate:
	}
	succeeded := false
	defer func() {
		if !succeeded {
			gate <- struct{}{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if lspa.closed {
		return nil, nil, fmt.Errorf("LSP activities are closed")
	}
	key, err := clientCacheKey(baseDir, lang, envContainer)
	if err != nil {
		return nil, nil, err
	}
	lspa.idleGeneration++
	if lspa.idleTimer != nil {
		lspa.idleTimer.Stop()
	}
	if lspa.InitializedClients == nil {
		lspa.InitializedClients = make(map[string]LSPClient)
	}
	if lspa.clientBorn.IsZero() {
		lspa.clientBorn = time.Now()
	}
	lspClient, ok := lspa.InitializedClients[key]
	if !ok || time.Since(lspa.clientBorn) >= lspa.maxClientAge {
		if err := lspa.closeClients(); err != nil {
			return nil, nil, fmt.Errorf("close previous analysis client: %w", err)
		}
		span.SetAttributes(attribute.Bool("initialized", true))
		lspClient = lspa.LSPClientProvider(lang)
		if client, ok := lspClient.(interface{ SetEnvContainer(*env.EnvContainer) }); ok {
			client.SetEnvContainer(envContainer)
		}
		params := InitializeParams{
			ProcessID:    0,
			RootURI:      (&url.URL{Scheme: "file", Path: baseDir}).String(),
			Capabilities: defaultClientCapabilities,
		}
		if _, err := lspClient.Initialize(ctx, params); err != nil {
			_ = closeClient(lspClient)
			err = fmt.Errorf("failed to initialize LSP client: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, nil, err
		}
		lspa.InitializedClients[key] = lspClient
		lspa.clientBorn = time.Now()
	} else {
		span.SetAttributes(attribute.Bool("cached", true))
	}
	release := lspa.releaseClient
	if envContainer != nil && envContainer.Env != nil && envContainer.Env.GetType() == env.EnvTypeModal {
		release = lspa.releaseModalClient
	}
	succeeded = true
	return lspClient, release, nil
}

// findSymbolPosition finds the position of the last character of the first matching text in the file content
func findSymbolPosition(reader io.Reader, fileRange *Range, symbolText string) (Position, error) {
	scanner := bufio.NewScanner(reader)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if fileRange != nil && (lineNum < fileRange.Start.Line || lineNum > fileRange.End.Line) {
			lineNum++
			continue
		}
		index := strings.Index(line, symbolText)
		if index != -1 {
			return Position{
				Line:      lineNum,
				Character: index + len(symbolText) - 1,
			}, nil
		}
		lineNum++
	}
	return Position{}, fmt.Errorf("symbol not found in file content")
}
