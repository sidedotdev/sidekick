package main

import (
	"os"
	"sidekick/common"
	"sidekick/worker"

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

	worker.StartWorker(common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())
}
