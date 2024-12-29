package nats

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/common"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ServerOptions contains configurable parameters for the NATS server
type ServerOptions struct {
	// Port is the port number the NATS server will listen on
	Port int

	// JetStreamDomain is the domain name for JetStream
	JetStreamDomain string

	// StoreDir is the directory where JetStream data will be stored
	StoreDir string

	// ServerName is an optional name for the server instance
	ServerName string

	// JetStreamMaxMemory is the maximum memory in bytes that can be used for JetStream
	// Optional: defaults to 1GB if not specified
	JetStreamMaxMemory int64

	// JetStreamMaxStore is the maximum disk space in bytes that can be used for JetStream
	// Optional: defaults to 20GB if not specified
	JetStreamMaxStore int64
}

// Server wraps a NATS server instance with Sidekick-specific configuration
type Server struct {
	natsServer *server.Server
	log        zerolog.Logger
	startOnce  sync.Once
}

var serve *Server
var serveOnce sync.Once = sync.Once{}

// A singleton instance of the NATS server
func GetOrNewServer() (*Server, error) {
	if serve == nil {
		var err error
		serveOnce.Do(func() {
			serve, err = newServer()
		})
		if err != nil {
			return nil, err
		}
	}
	return serve, nil
}

// newServer creates a new NATS server instance configured for Sidekick with default production settings
func newServer() (*Server, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
	}

	opts := ServerOptions{
		Port:            common.GetNatsServerPort(),
		JetStreamDomain: "sidekick_embedded",
		StoreDir:        filepath.Join(dataHome, "nats-jetstream"),
		ServerName:      "sidekick_embedded_nats_server",
	}

	return newServerWithOptions(opts)
}

// NewTestServer creates a new NATS server instance with custom options for testing
func NewTestServer(opts ServerOptions) (*Server, error) {
	return newServerWithOptions(opts)
}

// newServerWithOptions creates a new NATS server instance with the given options
func newServerWithOptions(opts ServerOptions) (*Server, error) {
	// Set default values for optional fields if not specified
	if opts.JetStreamMaxMemory == 0 {
		opts.JetStreamMaxMemory = 1024 * 1024 * 1024 // 1GB
	}
	if opts.JetStreamMaxStore == 0 {
		opts.JetStreamMaxStore = 20 * 1024 * 1024 * 1024 // 20GB
	}

	// Configure NATS server
	serverOpts := &server.Options{
		ServerName:         opts.ServerName,
		JetStream:          true,
		JetStreamDomain:    opts.JetStreamDomain,
		StoreDir:           opts.StoreDir,
		JetStreamMaxMemory: opts.JetStreamMaxMemory,
		JetStreamMaxStore:  opts.JetStreamMaxStore,
		Port:               opts.Port,
		// we'll always use the port to communication, never in-process.
		// simplifies development when we have multiple binaries that need to
		// communicate over nats/jetstream
		DontListen: false,
	}

	// Create NATS server instance
	natsServer, err := server.NewServer(serverOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create NATS server: %w", err)
	}

	// Configure server logging
	natsServer.SetLogger(newNATSLogger(), false, false)
	natsServer.ClientURL()

	return &Server{
		natsServer: natsServer,
		log:        log.With().Str("component", "nats-server").Logger(),
	}, nil
}

// Start starts the NATS server
func (s *Server) Start(ctx context.Context) error {
	s.startOnce.Do(func() {
		s.natsServer.Start()
	})

	// Wait for server to be ready
	if !s.natsServer.ReadyForConnections(5 * time.Second) {
		return fmt.Errorf("NATS server failed to start within 5s timeout")
	}

	return nil
}

// Stop gracefully stops the NATS server
func (s *Server) Stop() error {
	s.natsServer.LameDuckShutdown()
	return nil
}

// newNATSLogger creates a NATS-compatible logger that forwards to zerolog
func newNATSLogger() server.Logger {
	return &natsLogger{
		log: log.With().Str("component", "nats").Logger().Level(zerolog.WarnLevel),
	}
}

// natsLogger implements the NATS Logger interface using zerolog
type natsLogger struct {
	log zerolog.Logger
}

func (n *natsLogger) Noticef(format string, v ...interface{}) {
	n.log.Info().Msgf(format, v...)
}

func (n *natsLogger) Warnf(format string, v ...interface{}) {
	n.log.Warn().Msgf(format, v...)
}

func (n *natsLogger) Fatalf(format string, v ...interface{}) {
	n.log.Fatal().Msgf(format, v...)
}

func (n *natsLogger) Errorf(format string, v ...interface{}) {
	n.log.Error().Msgf(format, v...)
}

func (n *natsLogger) Debugf(format string, v ...interface{}) {
	n.log.Debug().Msgf(format, v...)
}

func (n *natsLogger) Tracef(format string, v ...interface{}) {
	n.log.Trace().Msgf(format, v...)
}
