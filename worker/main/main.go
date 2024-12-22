package main

import (
	"os"
	"os/signal"
	"sidekick/common"
	"sidekick/worker"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Load the .env file (You can do this once and cache if needed)
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Error loading .env file")
		}
	}

	w := worker.StartWorker(common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// graceful shutdown
	w.Stop()
}
