package nats

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/common"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Server wraps a NATS server instance with Sidekick-specific configuration
type Server struct {
	natsServer *server.Server
	log        zerolog.Logger
}

// New creates a new NATS server instance configured for Sidekick
func New() (*Server, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
	}

	// Create JetStream directory
	jetstreamDir := filepath.Join(dataHome, "nats-jetstream")

	// Configure NATS server
	opts := &server.Options{
		JetStream: true,
		StoreDir:  jetstreamDir,
		Port:      -1,                            // Disable client connections
		HTTPPort:  -1,                            // Disable monitoring
		Cluster:   server.ClusterOpts{Port: -1},  // Disable clustering
		LeafNode:  server.LeafNodeOpts{Port: -1}, // Disable leaf nodes
	}

	// Create NATS server instance
	natsServer, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create NATS server: %w", err)
	}

	// Configure server logging
	natsServer.SetLogger(newNATSLogger(), true, true)

	return &Server{
		natsServer: natsServer,
		log:        log.With().Str("component", "nats").Logger(),
	}, nil
}

// Start starts the NATS server
func (s *Server) Start(ctx context.Context) error {
	s.log.Info().Msg("Starting NATS server...")

	if err := s.natsServer.Start(); err != nil {
		return fmt.Errorf("failed to start NATS server: %w", err)
	}

	// Wait for server to be ready
	if !s.natsServer.ReadyForConnections(4) {
		return fmt.Errorf("NATS server failed to start within timeout")
	}

	s.log.Info().Msg("NATS server started successfully")
	return nil
}

// Stop gracefully stops the NATS server
func (s *Server) Stop() error {
	s.log.Info().Msg("Stopping NATS server...")
	s.natsServer.Shutdown()
	s.log.Info().Msg("NATS server stopped")
	return nil
}

// newNATSLogger creates a NATS-compatible logger that forwards to zerolog
func newNATSLogger() server.Logger {
	return &natsLogger{
		log: log.With().Str("component", "nats").Logger(),
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
