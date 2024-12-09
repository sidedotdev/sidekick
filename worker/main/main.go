package main

import (
	"flag"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"os"
)
import (
	"sidekick/worker" // Correctly pathing to the worker package

	"github.com/joho/godotenv"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	// Load the .env file (You can do this once and cache if needed)
	if err := godotenv.Load(); err != nil {
		log.Fatal().Err(err).Msg("dot env loading failed")
	}

	var hostPort string
	var taskQueue string
	flag.StringVar(&hostPort, "hostPort", client.DefaultHostPort, "Host and port for the Temporal server, eg localhost:7233")
	flag.StringVar(&taskQueue, "taskQueue", "default", "Task queue to use, eg default")
	flag.Parse()

	worker.StartWorker(hostPort, taskQueue)
}
