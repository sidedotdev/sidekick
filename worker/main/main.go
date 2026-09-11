package main

import (
	"context"
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
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel)

	// Load the .env file (You can do this once and cache if needed)
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Error loading .env file")
		}
	}

	common.StartPprofServer()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w, err := worker.StartWorker(ctx, common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())
	if err != nil {
		log.Info().Err(err).Msg("Worker startup aborted")
		return
	}

	<-ctx.Done()

	// graceful shutdown
	w.Stop()
}
