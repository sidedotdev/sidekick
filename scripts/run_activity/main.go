package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sidekick"
	"strings"
	"time"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/dev"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/poll_failures"
	"sidekick/srv"
	"sidekick/workspace"

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

func getFunctionName(fn interface{}) string {
	fullName := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	parts := strings.Split(fullName, ".")
	name := parts[len(parts)-1]
	// Remove -fm suffix that Go adds for method values
	name = strings.TrimSuffix(name, "-fm")
	return name
}

func buildActivityRegistry() map[string]interface{} {
	registry := make(map[string]interface{})

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage for direct execution")
	}

	featureFlag, err := fflag.NewFFlag("flags.yml")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to create go-feature-flag instance, some activities may not work")
	}
	ffa := fflag.FFlagActivities{FFlag: featureFlag}

	hostPort := common.GetTemporalServerHostPort()
	tracingInterceptor, _ := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	temporalClient, err := client.Dial(client.Options{
		HostPort:     hostPort,
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to create Temporal client, some activities may not work")
	}

	devManagerActivities := &dev.DevAgentManagerActivities{
		Storage:        service,
		TemporalClient: temporalClient,
	}
	flowActivities := &flow_action.FlowActivities{Service: service}
	embedActivities := &persisted_ai.EmbedActivities{
		Storage: service,
	}
	llmActivities := &persisted_ai.LlmActivities{
		Streamer: service,
	}
	llm2Activities := &persisted_ai.Llm2Activities{
		Streamer: service,
		Storage:  service,
	}
	lspActivities := &lsp.LSPActivities{
		LSPClientProvider: func(languageName string) lsp.LSPClient {
			return &lsp.Jsonrpc2LSPClient{
				LanguageName: languageName,
			}
		},
		InitializedClients: map[string]lsp.LSPClient{},
	}
	treeSitterActivities := &tree_sitter.TreeSitterActivities{
		DatabaseAccessor: service,
	}
	codingActivities := &coding.CodingActivities{
		TreeSitterActivities: treeSitterActivities,
		LSPActivities:        lspActivities,
	}
	readImageActivities := &persisted_ai.ReadImageActivities{
		Storage: service,
	}
	vectorActivities := &persisted_ai.VectorActivities{
		DatabaseAccessor: service,
	}
	ragActivities := &persisted_ai.RagActivities{
		DatabaseAccessor: service,
	}
	pollFailuresActivities := &poll_failures.PollFailuresActivities{
		TemporalClient: temporalClient,
		Service:        service,
	}
	devActivities := &dev.DevActivities{
		LSPActivities: lspActivities,
	}
	devRunActivities := &dev.DevRunActivities{
		Streamer: service,
	}
	srvActivities := &srv.Activities{Service: service}
	workspaceActivities := &workspace.Activities{Storage: service}

	// Register standalone functions
	standaloneFuncs := []interface{}{
		env.NewLocalGitWorktreeActivity,
		sidekick.GithubCloneRepoActivity,
		env.EnvRunCommandActivity,
		git.GitDiffActivity,
		git.DiffUntrackedFilesActivity,
		git.GitAddActivity,
		git.GitRestoreActivity,
		git.GitCommitActivity,
		git.GetGitUserConfigActivity,
		git.GitCheckoutActivity,
		git.GitMergeActivity,
		git.ListWorktreesActivity,
		git.CleanupWorktreeActivity,
		git.GetCurrentBranch,
		git.GetDefaultBranch,
		git.ListLocalBranches,
		git.WriteTreeActivity,
		dev.GetRepoConfigActivity,
		dev.GetRepoConfigActivityV2,
		dev.GetSymbolsActivity,
		dev.ApplyEditBlocksActivity,
		dev.ReadFileActivity,
		dev.BulkReadFileActivity,
		dev.ManageChatHistoryActivity,
		dev.ManageChatHistoryV2Activity,
		common.GetLocalConfig,
		common.BaseCommandPermissionsActivity,
	}
	for _, fn := range standaloneFuncs {
		registry[getFunctionName(fn)] = fn
	}

	// Register struct methods
	registerStructMethods(registry, srvActivities)
	registerStructMethods(registry, llmActivities)
	registerStructMethods(registry, llm2Activities)
	registerStructMethods(registry, pollFailuresActivities)
	registerStructMethods(registry, lspActivities)
	registerStructMethods(registry, treeSitterActivities)
	registerStructMethods(registry, codingActivities)
	registerStructMethods(registry, ragActivities)
	registerStructMethods(registry, embedActivities)
	registerStructMethods(registry, vectorActivities)
	registerStructMethods(registry, flowActivities)
	registerStructMethods(registry, devManagerActivities)
	registerStructMethods(registry, devActivities)
	registerStructMethods(registry, devRunActivities)
	registerStructMethods(registry, readImageActivities)
	registerStructMethods(registry, workspaceActivities)
	registry["EvalBoolFlag"] = ffa.EvalBoolFlag

	return registry
}

func registerStructMethods(registry map[string]interface{}, structPtr interface{}) {
	val := reflect.ValueOf(structPtr)
	typ := val.Type()
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		if method.IsExported() {
			registry[method.Name] = val.Method(i).Interface()
		}
	}
}

func executeActivityDirect(activityName string, activityArgs []json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	registry := buildActivityRegistry()

	activityFn, ok := registry[activityName]
	if !ok {
		return nil, fmt.Errorf("activity %q not found in registry", activityName)
	}

	fnVal := reflect.ValueOf(activityFn)
	fnType := fnVal.Type()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build arguments: first arg is context, rest are from JSON
	// FIXME only do this if it actually is the first argument, it does not have to be
	args := make([]reflect.Value, fnType.NumIn())
	args[0] = reflect.ValueOf(ctx)

	for i := 1; i < fnType.NumIn(); i++ {
		argType := fnType.In(i)
		argPtr := reflect.New(argType)
		if i-1 < len(activityArgs) {
			if err := json.Unmarshal(activityArgs[i-1], argPtr.Interface()); err != nil {
				return nil, fmt.Errorf("failed to unmarshal argument %d: %w", i-1, err)
			}
		}
		args[i] = argPtr.Elem()
	}

	results := fnVal.Call(args)

	// Handle return values: (result, error) or just (error)
	var resultVal reflect.Value
	var errVal reflect.Value
	if len(results) == 2 {
		resultVal = results[0]
		errVal = results[1]
	} else if len(results) == 1 {
		errVal = results[0]
	}

	if !errVal.IsNil() {
		return nil, errVal.Interface().(error)
	}

	if resultVal.IsValid() {
		resultJSON, err := json.Marshal(resultVal.Interface())
		if err != nil {
			return nil, fmt.Errorf("failed to marshal result: %w", err)
		}
		return resultJSON, nil
	}

	return json.RawMessage("null"), nil
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var timeout time.Duration
	var direct bool
	flag.DurationVar(&timeout, "timeout", 180*time.Second, "Timeout for the activity execution")
	flag.BoolVar(&direct, "direct", true, "Execute activity directly without Temporal workflow")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--timeout duration] [--direct] <activity_name> <json_file_path>\n", os.Args[0])
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

	var result json.RawMessage

	if direct {
		log.Info().Str("activity", activityName).Msg("Executing activity directly")
		result, err = executeActivityDirect(activityName, activityArgs, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error executing activity: %v\n", err)
			os.Exit(1)
		}
	} else {
		result, err = executeActivityViaWorkflow(activityName, activityArgs, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error executing activity via workflow: %v\n", err)
			os.Exit(1)
		}
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

func executeActivityViaWorkflow(activityName string, activityArgs []json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	hostPort := common.GetTemporalServerHostPort()
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating tracing interceptor: %w", err)
	}
	temporalClient, err := client.Dial(client.Options{
		HostPort:     hostPort,
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	})
	if err != nil {
		return nil, fmt.Errorf("error connecting to Temporal: %w", err)
	}
	defer temporalClient.Close()

	w := worker.New(temporalClient, runActivityTaskQueue, worker.Options{})
	w.RegisterWorkflow(RunActivityWorkflow)
	err = w.Start()
	if err != nil {
		return nil, fmt.Errorf("error starting worker: %w", err)
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
		return nil, fmt.Errorf("error executing workflow: %w", err)
	}

	var result json.RawMessage
	err = workflowRun.Get(ctx, &result)
	if err != nil {
		return nil, fmt.Errorf("error getting workflow result: %w", err)
	}

	return result, nil
}
