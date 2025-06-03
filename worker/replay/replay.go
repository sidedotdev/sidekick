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

	// Define flag sets for subcommands
	// store subcommand
	storeCmd := flag.NewFlagSet("store", flag.ExitOnError)
	var storeHostPort, storeTaskQueue, storeWorkflowId, storeSidekickVersion string
	storeCmd.StringVar(&storeHostPort, "hostPort", common.GetTemporalServerHostPort(), "Host and port for the Temporal server (for store command)")
	storeCmd.StringVar(&storeTaskQueue, "taskQueue", "default", "Task queue to use (for store command)")
	storeCmd.StringVar(&storeWorkflowId, "id", "", "Workflow ID to store (mandatory for store command)")
	storeCmd.StringVar(&storeSidekickVersion, "sidekick-version", "", "Sidekick version (mandatory for store command)")

	// run-from-s3 subcommand
	runFromS3Cmd := flag.NewFlagSet("run-from-s3", flag.ExitOnError)
	var runFromS3WorkflowId, runFromS3SidekickVersion string
	runFromS3Cmd.StringVar(&runFromS3WorkflowId, "id", "", "Workflow ID to run from S3 (mandatory for run-from-s3 command)")
	runFromS3Cmd.StringVar(&runFromS3SidekickVersion, "sidekick-version", "", "Sidekick version (mandatory for run-from-s3 command)")

	// Default command flags
	var defaultHostPort, defaultTaskQueue, defaultWorkflowId string
	flag.StringVar(&defaultHostPort, "hostPort", common.GetTemporalServerHostPort(), "Host and port for the Temporal server, eg localhost:7233 (default command)")
	flag.StringVar(&defaultTaskQueue, "taskQueue", "default", "Task queue to use, eg default (default command)")
	flag.StringVar(&defaultWorkflowId, "id", "", "Workflow ID to replay (default command, mandatory if no subcommand)")

	// Custom usage messages
	storeCmd.Usage = func() {
		//nolint:errcheck,lll,goconst
		log.Error().Msg("Usage: replay store -id <workflow_id> -sidekick-version <version> [-hostPort <host:port>] [-taskQueue <queue_name>]")
		storeCmd.PrintDefaults()
	}
	runFromS3Cmd.Usage = func() {
		//nolint:errcheck,lll,goconst
		log.Error().Msg("Usage: replay run-from-s3 -id <workflow_id> -sidekick-version <version>")
		runFromS3Cmd.PrintDefaults()
	}
	flag.Usage = func() {
		//nolint:errcheck,lll,goconst
		log.Error().Msg("Usage: replay [-id <workflow_id>] [-hostPort <host:port>] [-taskQueue <queue_name>]")
		log.Error().Msg("Or: replay <subcommand> [options]")
		log.Error().Msg("Subcommands:")
		log.Error().Msg("  store          Fetches workflow history and stores it to S3.")
		log.Error().Msg("  run-from-s3    Replays workflow history from S3 (via local cache).")
		log.Error().Msg("\nDefault command flags (if no subcommand is given):")
		flag.PrintDefaults()
		log.Error().Msgf("\nFor 'store' subcommand usage:\nreplay store --help")
		log.Error().Msgf("\nFor 'run-from-s3' subcommand usage:\nreplay run-from-s3 --help")
	}

	flag.Parse()

	if flag.NArg() > 0 {
		subcommand := flag.Arg(0)
		args := flag.Args()[1:]

		switch subcommand {
		case "store":
			if err := storeCmd.Parse(args); err != nil {
				log.Error().Err(err).Msg("Error parsing 'store' subcommand flags.")
				storeCmd.Usage() // Show specific usage for store
				os.Exit(1)
			}
			if storeWorkflowId == "" {
				log.Error().Msg("Error: -id is required for 'store' subcommand.")
				storeCmd.Usage()
				os.Exit(1)
			}
			if storeSidekickVersion == "" {
				log.Error().Msg("Error: -sidekick-version is required for 'store' subcommand.")
				storeCmd.Usage()
				os.Exit(1)
			}
			log.Info().Msgf("Executing 'store' command: id=%s, hostPort=%s, taskQueue=%s, sidekick-version=%s", storeWorkflowId, storeHostPort, storeTaskQueue, storeSidekickVersion)
			// Placeholder for store logic implementation (Step 4)
			// Example: storeWorkflow(storeWorkflowId, storeHostPort, storeTaskQueue, storeSidekickVersion)
		case "run-from-s3":
			if err := runFromS3Cmd.Parse(args); err != nil {
				log.Error().Err(err).Msg("Error parsing 'run-from-s3' subcommand flags.")
				runFromS3Cmd.Usage() // Show specific usage for run-from-s3
				os.Exit(1)
			}
			if runFromS3WorkflowId == "" {
				log.Error().Msg("Error: -id is required for 'run-from-s3' subcommand.")
				runFromS3Cmd.Usage()
				os.Exit(1)
			}
			if runFromS3SidekickVersion == "" {
				log.Error().Msg("Error: -sidekick-version is required for 'run-from-s3' subcommand.")
				runFromS3Cmd.Usage()
				os.Exit(1)
			}
			log.Info().Msgf("Executing 'run-from-s3' command: id=%s, sidekick-version=%s", runFromS3WorkflowId, runFromS3SidekickVersion)
			// Placeholder for run-from-s3 logic implementation (Step 5)
			// Example: runReplayFromS3(runFromS3WorkflowId, runFromS3SidekickVersion)
		default:
			log.Error().Msgf("Unknown subcommand: %s", subcommand)
			flag.Usage() // Show global usage
			os.Exit(1)
		}
	} else {
		// Default command (original behavior)
		if defaultWorkflowId == "" {
			log.Error().Msg("Error: -id is required for default replay command (or specify a subcommand).")
			flag.Usage() // Show global usage
			os.Exit(1)
		}

		log.Info().Msgf("Executing default replay: id=%s, hostPort=%s, taskQueue=%s", defaultWorkflowId, defaultHostPort, defaultTaskQueue)

		clientOptions := client.Options{
			Logger:   logur.LoggerToKV(zerologadapter.New(log.Logger)),
			HostPort: defaultHostPort,
		}
		c, err := client.Dial(clientOptions)
		if err != nil {
			log.Fatal().Err(err).Msg("Unable to create Temporal client for default replay.")
		}
		defer c.Close()

		if err := ReplayWorkflowLatest(context.Background(), c, defaultWorkflowId); err != nil {
			log.Fatal().Err(err).Msg("Default replay failed.")
		}
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
