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

// Correctly pathing to the worker package

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	// Load the .env file (You can do this once and cache if needed)
	if err := godotenv.Load(); err != nil {
		log.Fatal().Err(err).Msg("dot env loading failed")
	}

	_ = worker.StartWorker(common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
