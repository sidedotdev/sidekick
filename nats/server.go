package nats

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/common"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Server wraps a NATS server instance with Sidekick-specific configuration
type Server struct {
	natsServer *server.Server
	log        zerolog.Logger
}

// NewServer creates a new NATS server instance configured for Sidekick
func NewServer() (*Server, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
	}

	// Configure NATS server
	opts := &server.Options{
		ServerName: "sidekick_embedded_nats_server",

		JetStream:          true,
		JetStreamDomain:    "sidekick_embedded",
		StoreDir:           filepath.Join(dataHome, "nats-jetstream"),
		JetStreamMaxMemory: 1024 * 1024 * 1024,      // 1GB
		JetStreamMaxStore:  20 * 1024 * 1024 * 1024, // 20GB
		Port:               28855, // TODO add to hosts_and_ports.go

		// we'll always use the port to communication, never in-process.
		// simplifies development when we have multiple binaries that need to
		// communicate over nats/jetstream
		DontListen: false,
	}

	// Create NATS server instance
	natsServer, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create NATS server: %w", err)
	}

	// Configure server logging
	natsServer.SetLogger(newNATSLogger(), false, false)

	return &Server{
		natsServer: natsServer,
		log:        log.With().Str("component", "nats-server").Logger(),
	}, nil
}

// Start starts the NATS server
func (s *Server) Start(ctx context.Context) error {
	s.natsServer.Start()

	// Wait for server to be ready
	if !s.natsServer.ReadyForConnections(5 * time.Second) {
		return fmt.Errorf("NATS server failed to start within timeout")
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
