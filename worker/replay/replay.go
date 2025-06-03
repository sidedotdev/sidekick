package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"sidekick/common"
	"sidekick/utils" // Added for S3 utilities
	sidekick_worker "sidekick/worker"

	"github.com/aws/aws-sdk-go-v2/service/s3"
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
			if err := handleStoreCommand(storeWorkflowId, storeHostPort, storeTaskQueue, storeSidekickVersion); err != nil {
				log.Fatal().Err(err).Msg("Store command execution failed.")
			}
			log.Info().Msgf("Store command for workflow %s (version %s) completed successfully.", storeWorkflowId, storeSidekickVersion)
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
			if err := handleRunFromS3Command(runFromS3WorkflowId, runFromS3SidekickVersion); err != nil {
				log.Fatal().Err(err).Msg("Run-from-S3 command execution failed.")
			}
			log.Info().Msgf("Run-from-S3 command for workflow %s (version %s) completed successfully.", runFromS3WorkflowId, runFromS3SidekickVersion)
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

func handleStoreCommand(workflowId, hostPort, taskQueue, sidekickVersion string) error {
	log.Info().Msgf("Initiating store command for workflow ID: %s, version: %s", workflowId, sidekickVersion)

	// Initialize Temporal client
	clientOptions := client.Options{
		Logger:   logur.LoggerToKV(zerologadapter.New(log.Logger)),
		HostPort: hostPort,
	}
	c, err := client.Dial(clientOptions)
	if err != nil {
		return fmt.Errorf("unable to create Temporal client for store command (hostPort: %s): %w", hostPort, err)
	}
	defer c.Close()
	log.Info().Str("hostPort", hostPort).Msg("Temporal client created for store command")

	// Describe workflow execution to get the latest RunID
	ctx := context.Background()
	desc, err := c.DescribeWorkflowExecution(ctx, workflowId, "") // Empty runID for latest
	if err != nil {
		return fmt.Errorf("failed to describe workflow execution %s: %w", workflowId, err)
	}
	runID := desc.WorkflowExecutionInfo.Execution.GetRunId()
	if runID == "" {
		return fmt.Errorf("failed to get a valid runID for workflow %s (execution status: %s)", workflowId, desc.WorkflowExecutionInfo.GetStatus().String())
	}
	log.Info().Str("workflowId", workflowId).Str("runId", runID).Msg("Latest run ID fetched")

	// Get workflow history
	hist, err := GetWorkflowHistory(ctx, c, workflowId, runID)
	if err != nil {
		return fmt.Errorf("failed to get workflow history for %s (run %s): %w", workflowId, runID, err)
	}
	log.Info().Str("workflowId", workflowId).Str("runId", runID).Int("eventCount", len(hist.Events)).Msg("Workflow history fetched")

	// Serialize history to JSON
	jsonData, err := json.MarshalIndent(hist, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize workflow history to JSON: %w", err)
	}
	log.Debug().Msg("Workflow history serialized to JSON")

	// Initialize S3 client
	s3Client, err := utils.NewS3Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}
	log.Info().Msg("S3 client initialized")

	// Construct S3 bucket, key, and metadata
	s3Bucket := "genflow.dev"
	s3Key := fmt.Sprintf("sidekick/replays/%s/%s_events.json", sidekickVersion, workflowId)
	metadata := map[string]string{
		"workflow-id":      workflowId,
		"sidekick-version": sidekickVersion,
		"hostPort":         hostPort,
		"taskQueue":        taskQueue,
	}
	log.Info().Str("bucket", s3Bucket).Str("key", s3Key).Interface("metadata", metadata).Msg("Preparing to upload to S3")

	// Upload JSON to S3
	err = utils.UploadJSONWithMetadata(ctx, s3Client, s3Bucket, s3Key, jsonData, metadata)
	if err != nil {
		return fmt.Errorf("failed to upload workflow history to S3 (bucket: %s, key: %s): %w", s3Bucket, s3Key, err)
	}

	log.Info().Str("bucket", s3Bucket).Str("key", s3Key).Msg("Successfully uploaded workflow history to S3")
	return nil
}

// fetchAndCacheHistory retrieves workflow history, utilizing a local cache.
// If the history is not in the cache or the cached version is corrupted, it downloads from S3 and updates the cache.
func fetchAndCacheHistory(ctx context.Context, s3Client *s3.Client, workflowID string, sidekickVersion string) (*history.History, error) {
	cachePath, err := common.GetReplayCacheFilePath(sidekickVersion, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to get replay cache file path for %s (version %s): %w", workflowID, sidekickVersion, err)
	}

	// Attempt to read from cache
	cachedData, err := os.ReadFile(cachePath)
	if err == nil {
		var hist history.History
		if err := json.Unmarshal(cachedData, &hist); err == nil {
			log.Info().Str("workflowId", workflowID).Str("version", sidekickVersion).Str("cachePath", cachePath).Msg("Workflow history successfully loaded from local cache.")
			return &hist, nil
		}
		log.Warn().Err(err).Str("workflowId", workflowID).Str("cachePath", cachePath).Msg("Failed to unmarshal cached history, will attempt S3 download.")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read cache file %s for workflow %s (version %s): %w", cachePath, workflowID, sidekickVersion, err)
	} else {
		log.Info().Str("workflowId", workflowID).Str("version", sidekickVersion).Str("cachePath", cachePath).Msg("Workflow history not found in local cache, attempting S3 download.")
	}

	// Download from S3
	s3Bucket := "genflow.dev"
	s3Key := fmt.Sprintf("sidekick/replays/%s/%s_events.json", sidekickVersion, workflowID)

	jsonData, err := utils.DownloadObject(ctx, s3Client, s3Bucket, s3Key)
	if err != nil {
		return nil, fmt.Errorf("failed to download history from S3 (bucket: %s, key: %s) for workflow %s (version %s): %w", s3Bucket, s3Key, workflowID, sidekickVersion, err)
	}
	log.Info().Str("workflowId", workflowID).Str("version", sidekickVersion).Str("s3Key", s3Key).Msg("Workflow history downloaded from S3.")

	// Write to cache
	if err := os.WriteFile(cachePath, jsonData, 0644); err != nil {
		// Log a warning but proceed, as we have the data in memory
		log.Warn().Err(err).Str("workflowId", workflowID).Str("cachePath", cachePath).Msg("Failed to write downloaded history to cache.")
	} else {
		log.Info().Str("workflowId", workflowID).Str("version", sidekickVersion).Str("cachePath", cachePath).Msg("Workflow history successfully written to local cache.")
	}

	// Deserialize and return
	var hist history.History
	if err := json.Unmarshal(jsonData, &hist); err != nil {
		return nil, fmt.Errorf("failed to unmarshal downloaded S3 history for workflow %s (version %s): %w", workflowID, sidekickVersion, err)
	}

	return &hist, nil
}

func handleRunFromS3Command(workflowId, sidekickVersion string) error {
	log.Info().Msgf("Initiating run-from-s3 command for workflow ID: %s, version: %s", workflowId, sidekickVersion)
	ctx := context.Background()

	s3Client, err := utils.NewS3Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create S3 client for run-from-s3: %w", err)
	}
	log.Info().Msg("S3 client initialized for run-from-s3.")

	hist, err := fetchAndCacheHistory(ctx, s3Client, workflowId, sidekickVersion)
	if err != nil {
		return fmt.Errorf("failed to fetch and cache history for workflow %s (version %s): %w", workflowId, sidekickVersion, err)
	}

	replayer := worker.NewWorkflowReplayer()
	sidekick_worker.RegisterWorkflows(replayer)
	log.Info().Str("workflowId", workflowId).Str("version", sidekickVersion).Msg("Workflow replayer initialized and workflows registered.")

	if err := replayer.ReplayWorkflowHistory(nil, hist); err != nil {
		return fmt.Errorf("workflow history replay failed for %s (version %s): %w", workflowId, sidekickVersion, err)
	}

	log.Info().Str("workflowId", workflowId).Str("version", sidekickVersion).Msg("Workflow history replayed successfully from S3/cache.")
	return nil
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
