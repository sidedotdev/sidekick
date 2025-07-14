package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/nats"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv/jetstream"
	sidekickworker "sidekick/worker" // Added for StartWorker
	"syscall"
	"time"

	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// SentimentRequest defines the structure for sentiment analysis requests.
type SentimentRequest struct {
	Analysis  string `json:"analysis" jsonschema:"description=Very brief analysis of the sentiment of the user's message"`
	Sentiment string `json:"sentiment" jsonschema:"enum=positive,enum=negative"`
}

// ExampleLlmActivitiesWorkflow demonstrates an LLM activity within a Temporal workflow.
func ExampleLlmActivitiesWorkflow(ctx workflow.Context) (string, error) {
	// RetryPolicy specifies how to automatically handle retries if an Activity fails.
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        100 * time.Second,
		MaximumAttempts:        1, // don't retry
		NonRetryableErrorTypes: []string{"SomeApplicationError", "AnotherApplicationError"},
	}

	activityOptions := workflow.ActivityOptions{
		// Timeout options specify when to automatically timeout Activity functions.
		StartToCloseTimeout: time.Minute,
		// Optionally provide a customized RetryPolicy.
		// Temporal retries failed Activities by default.
		RetryPolicy: retrypolicy,
	}

	// Apply the options.
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	chatStreamOptions := persisted_ai.ChatStreamOptions{
		WorkspaceId:  "<workspace_id>",   // Replace with valid value
		FlowId:       "<flow_id>",        // Replace with current flow ID
		FlowActionId: "<flow_action_id>", // Replace with action ID
		ToolChatOptions: llm.ToolChatOptions{
			Secrets: secret_manager.SecretManagerContainer{
				SecretManager: &secret_manager.EnvSecretManager{},
			},
			Params: llm.ToolChatParams{
				Messages: []llm.ChatMessage{
					{
						Role:    llm.ChatMessageRoleUser,
						Content: "That's cool.",
					},
				},
				ToolChoice: llm.ToolChoice{
					Type: llm.ToolChoiceTypeRequired,
				},
				Tools: []*llm.Tool{
					{
						Name:        "describe_sentiment",
						Description: "This tool is used to describe the sentiment of the user's message.",
						Parameters:  anthropic.GenerateInputSchema(&SentimentRequest{}),
					},
				},
				ModelConfig: common.ModelConfig{
					Provider: string(llm.AnthropicToolChatProviderType),
				},
			},
		},
	}

	var chatResponse llm.ChatMessageResponse
	// Create a real JetStream streamer for demonstration.
	nc, err := nats.GetConnection()
	if err != nil {
		return "", fmt.Errorf("failed to connect to NATS: %w", err)
	}
	streamer, err := jetstream.NewStreamer(nc)
	if err != nil {
		return "", fmt.Errorf("failed to create JetStream streamer: %w", err)
	}
	// This workflow instantiates LlmActivities directly with a real streamer.
	la := persisted_ai.LlmActivities{Streamer: streamer}
	err = workflow.ExecuteActivity(ctx, la.ChatStream, chatStreamOptions).Get(ctx, &chatResponse)
	if err != nil {
		return "", err
	}

	// Example of how to handle tool calls if needed, though this example expects a specific outcome.
	// if !(chatResponse.StopReason == string(openai.FinishReasonToolCalls) || chatResponse.StopReason == string(openai.FinishReasonStop)) {
	// 	return "", errors.New("Expected finish reason to be stop or tool calls")
	// }

	if len(chatResponse.ToolCalls) == 0 {
		return "", fmt.Errorf("Expected tool calls, but got none. Response: %+v", chatResponse)
	}

	jsonStr := chatResponse.ToolCalls[0].Arguments
	var result map[string]string
	err = json.Unmarshal([]byte(jsonStr), &result)
	if err != nil {
		return "", fmt.Errorf("Failed to unmarshall json to map[string]string: %v. JSON string was: %s", err, jsonStr)
	}

	if result["sentiment"] != "positive" {
		return "", fmt.Errorf("Expected sentiment to be positive, but it wasn't. Result: %v", result)
	}

	return jsonStr, nil
}

func main() {
	// Initialize zerolog for console output
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel)

	// Optionally load .env file
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			// Log warning if .env file exists but couldn't be loaded
			log.Warn().Err(err).Msg("Error loading .env file, proceeding without it")
		} else {
			// Log info if .env file simply doesn't exist
			log.Info().Msg(".env file not found, proceeding without it")
		}
	}

	// Initialize Temporal client
	clientOptions := client.Options{
		HostPort: common.GetTemporalServerHostPort(), // Assumes common.GetTemporalServerHostPort() is available
	}

	temporalClient, err := client.NewLazyClient(clientOptions)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Temporal lazy client")
	}
	defer temporalClient.Close()

	// Define a dedicated task queue name
	const taskQueueName = "example-llm-task-queue"

	// Create a new Temporal worker
	// For this example, we'll use default worker options.
	// If a custom logger was needed for the worker, it could be configured here:
	// workerLogger := logur.LoggerToKV(logurzerologadapter.New(log.Logger.With().Str("component", "temporal_worker").Logger()))
	// workerOptions := worker.Options{Logger: workerLogger}
	// w := worker.New(temporalClient, taskQueueName, workerOptions)

	// Use sidekick's StartWorker to initialize and start a worker with default activities.
	// StartWorker is blocking if not run in a goroutine, but for this example CLI,
	// it sets up the worker and then the main function proceeds to trigger a workflow.
	// StartWorker itself handles logging its start.
	// It uses common.GetTemporalServerHostPort() internally if an empty hostPort is passed,
	// but we pass it explicitly for clarity.
	w := sidekickworker.StartWorker(common.GetTemporalServerHostPort(), taskQueueName)
	log.Info().Msgf("Worker setup by StartWorker on task queue '%s'", taskQueueName)

	// Register ExampleLlmActivitiesWorkflow with this worker
	// Note: StartWorker already starts the worker, so no w.Start() call is needed here.
	w.RegisterWorkflow(ExampleLlmActivitiesWorkflow)

	// Handle OS interrupt signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	workflowDoneChan := make(chan bool) // true for success, false for error

	// Goroutine to trigger and wait for workflow completion
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("Panic in workflow execution goroutine")
				workflowDoneChan <- false // Signal completion (as failure)
			}
		}()

		workflowID := fmt.Sprintf("example-llm-workflow-%d", time.Now().UnixNano())
		startWorkflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: taskQueueName,
		}

		log.Info().Msgf("Executing workflow '%s'...", workflowID)
		workflowRun, err := temporalClient.ExecuteWorkflow(context.Background(), startWorkflowOptions, ExampleLlmActivitiesWorkflow)
		if err != nil {
			log.Error().Err(err).Msg("Failed to start workflow execution")
			workflowDoneChan <- false
			return
		}
		log.Info().Str("WorkflowID", workflowRun.GetID()).Str("RunID", workflowRun.GetRunID()).Msg("Workflow execution started")

		var result string
		err = workflowRun.Get(context.Background(), &result)
		if err != nil {
			log.Error().Err(err).Msg("Workflow execution failed")
			workflowDoneChan <- false
		} else {
			log.Info().Msgf("Workflow completed successfully. Result: %s", result)
			workflowDoneChan <- true
		}
	}()

	// Wait for workflow completion or OS signal
	select {
	case success := <-workflowDoneChan:
		if success {
			log.Info().Msg("Workflow execution finished successfully. Shutting down worker.")
		} else {
			log.Info().Msg("Workflow execution finished with an error. Shutting down worker.")
		}
	case sig := <-signalChan:
		log.Info().Msgf("Received signal: %v. Shutting down worker.", sig)
	}

	log.Info().Msg("Stopping worker...")
	w.Stop()
	log.Info().Msg("Worker stopped.")
}
