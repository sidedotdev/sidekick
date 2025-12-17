package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"sidekick/common"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const runActivityTaskQueue = "run-activity-script"

func RunActivityWorkflow(ctx workflow.Context, activityName string, args []json.RawMessage) (json.RawMessage, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		TaskQueue:           common.GetTemporalTaskQueue(),
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result json.RawMessage
	err := workflow.ExecuteActivity(ctx, activityName, argsToInterfaces(args)...).Get(ctx, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func argsToInterfaces(args []json.RawMessage) []interface{} {
	result := make([]interface{}, len(args))
	for i, arg := range args {
		result[i] = arg
	}
	return result
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var timeout time.Duration
	flag.DurationVar(&timeout, "timeout", 180*time.Second, "Timeout for the activity execution")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--timeout duration] <activity_name> <json_file_path>\n", os.Args[0])
		os.Exit(1)
	}

	activityName := args[0]
	jsonFilePath := args[1]

	inputBytes, err := os.ReadFile(jsonFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	var activityArgs []json.RawMessage
	if err := json.Unmarshal(inputBytes, &activityArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	hostPort := common.GetTemporalServerHostPort()
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating tracing interceptor: %v\n", err)
		os.Exit(1)
	}
	temporalClient, err := client.Dial(client.Options{
		HostPort:     hostPort,
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to Temporal: %v\n", err)
		os.Exit(1)
	}
	defer temporalClient.Close()

	w := worker.New(temporalClient, runActivityTaskQueue, worker.Options{})
	w.RegisterWorkflow(RunActivityWorkflow)
	err = w.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting worker: %v\n", err)
		os.Exit(1)
	}
	defer w.Stop()

	workflowID := fmt.Sprintf("run-activity-%s", uuid.New().String())
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	log.Info().Str("activity", activityName).Str("workflowID", workflowID).Msg("Executing activity")

	workflowRun, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: runActivityTaskQueue,
	}, RunActivityWorkflow, activityName, activityArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing workflow: %v\n", err)
		os.Exit(1)
	}

	var result json.RawMessage
	err = workflowRun.Get(ctx, &result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting workflow result: %v\n", err)
		os.Exit(1)
	}

	var prettyResult interface{}
	if err := json.Unmarshal(result, &prettyResult); err != nil {
		fmt.Println(string(result))
	} else {
		prettyJSON, err := json.MarshalIndent(prettyResult, "", "  ")
		if err != nil {
			fmt.Println(string(result))
		} else {
			fmt.Println(string(prettyJSON))
		}
	}
}
