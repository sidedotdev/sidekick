package main

import (
	"context"
	"os"
	"os/signal"
	"sidekick/api"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Error loading .env file")
		}
	}

	srv, shutdownTracer := api.RunServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// graceful shutdown
	ctx := context.Background()
	if err := shutdownTracer(ctx); err != nil {
		log.Error().Err(err).Msg("Error shutting down telemetry")
	}
	srv.Shutdown(ctx)
}
