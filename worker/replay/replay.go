package main

import (
	"context"
	"flag"
	"os"

	"sidekick/common"
	sidekick_worker "sidekick/worker"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	if err := godotenv.Load(); err != nil {
		log.Debug().Err(err).Msg("dot env loading failed")
	}

	var hostPort string
	var taskQueue string
	var workflowId string
	flag.StringVar(&hostPort, "hostPort", common.GetTemporalServerHostPort(), "Host and port for the Temporal server, eg localhost:7233")
	flag.StringVar(&taskQueue, "taskQueue", "default", "Task queue to use, eg default")
	flag.StringVar(&workflowId, "id", "", "Workflow ID to replay")
	flag.Parse()

	if workflowId == "" {
		log.Fatal().Msg("id is required")
	}

	clientOptions := client.Options{
		Logger:   logur.LoggerToKV(zerologadapter.New(log.Logger)),
		HostPort: hostPort,
	}
	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create Temporal client.")
	}
	defer c.Close()

	if err := ReplayWorkflowLatest(context.Background(), c, workflowId); err != nil {
		log.Fatal().Err(err).Msg("Replay failed")
	}
}

func GetWorkflowHistory(ctx context.Context, client client.Client, id, runID string) (*history.History, error) {
	var hist history.History
	iter := client.GetWorkflowHistory(ctx, id, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, err
		}
		hist.Events = append(hist.Events, event)
	}
	return &hist, nil
}

func ReplayWorkflow(ctx context.Context, client client.Client, id, runID string) error {
	hist, err := GetWorkflowHistory(ctx, client, id, runID)
	if err != nil {
		return err
	}
	replayer := worker.NewWorkflowReplayer()
	sidekick_worker.RegisterWorkflows(replayer)
	return replayer.ReplayWorkflowHistory(nil, hist)
}

func ReplayWorkflowLatest(ctx context.Context, client client.Client, id string) error {
	hist, err := GetWorkflowHistory(ctx, client, id, "")
	if err != nil {
		return err
	}
	replayer := worker.NewWorkflowReplayer()
	sidekick_worker.RegisterWorkflows(replayer)
	return replayer.ReplayWorkflowHistory(nil, hist)
}
