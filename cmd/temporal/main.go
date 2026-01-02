package main

import (
	"os"
	"os/signal"
	"sidekick/common"
	"sidekick/temporal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Str("host", common.GetTemporalServerHost()).Int("port", common.GetTemporalServerPort()).Msg("Starting Temporal server")

	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Error loading .env file")
		}
	}

	server := temporal.Start()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	server.Stop()
}
