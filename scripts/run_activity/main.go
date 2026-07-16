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
	"strconv"
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
	"sidekick/temporalmeta"
	"sidekick/workspace"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
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
	readImageActivities := &dev.ReadImageActivities{
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
	chatHistoryActivities := &persisted_ai.ChatHistoryActivities{
		Storage: service,
	}
	kvActivities := &common.KVActivities{
		Storage: service,
	}
	cascadeDeleteActivities := &srv.CascadeDeleteTaskActivities{
		Service:        service,
		TemporalClient: temporalClient,
	}
	temporalMetaActivities := &temporalmeta.TemporalMetaActivities{
		Client: temporalClient,
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
		env.CreateDevPodWorktreeActivity,
		env.DevPodUpActivity,
		env.CreateSandboxActivity,
		env.CheckSandboxActivity,
		env.StopSandboxActivity,
		env.DeleteSandboxActivity,
		env.SyncRepoToRemoteActivity,
		env.DeepenRepoActivity,
		env.SnapshotEnvironmentActivity,
		env.CreateRemoteWorktreeActivity,
		sidekick.GithubCloneRepoActivity,
		env.EnvRunCommandActivity,
		env.GetEnvironmentInfoActivity,
		git.GitDiffActivity,
		git.DiffUntrackedFilesActivity,
		git.GitAddActivity,
		git.GitRestoreActivity,
		git.GitCommitActivity,
		git.GetGitUserConfigActivity,
		git.GitCheckoutActivity,
		git.GitMergeActivity,
		git.GitMergeAbortActivity,
		git.GitMergeInProgressActivity,
		git.GitMergeIntoWorktreeActivity,
		git.GitTransferWorktreeChangesActivity,
		git.GitListUnmergedActivity,
		git.GitSnapshotConflictMarkersActivity,
		git.GitConflictResolutionDiffActivity,
		git.GitCommitMergeActivity,
		git.CleanupWorktreeActivity,
		git.GetCurrentBranch,
		git.GetDefaultBranch,
		git.ListLocalBranches,
		git.WriteTreeActivity,
		dev.GetRepoConfigActivity,
		dev.GetRepoConfigActivityV2,
		dev.GetSymbolsActivity,
		dev.ResolveToolNameMappingActivity,
		dev.ApplyEditBlocksActivity,
		dev.ReadFileActivity,
		dev.BulkReadFileActivity,
		dev.ManageChatHistoryActivity,
		dev.ManageChatHistoryV2Activity,
		dev.SummarizeDiffActivity,
		dev.AssessResolutionSubstantialityActivity,
		dev.CheckCommandPermissionActivity,
		dev.EnsureCoreIgnoreFileActivity,
		common.GetLocalConfig,
		common.BaseCommandPermissionsActivity,
		persisted_ai.RepairToolCallArgumentsActivity,
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
	registerStructMethods(registry, chatHistoryActivities)
	registerStructMethods(registry, kvActivities)
	registerStructMethods(registry, cascadeDeleteActivities)
	registerStructMethods(registry, temporalMetaActivities)
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

	// Only inject a context when the activity's first parameter is a
	// context.Context; some activities take their input struct directly.
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	injectContext := fnType.NumIn() > 0 && fnType.In(0) == contextType

	args := make([]reflect.Value, fnType.NumIn())
	firstJSONArg := 0
	if injectContext {
		args[0] = reflect.ValueOf(ctx)
		firstJSONArg = 1
	}

	for i := firstJSONArg; i < fnType.NumIn(); i++ {
		argType := fnType.In(i)
		argPtr := reflect.New(argType)
		jsonArgIndex := i - firstJSONArg
		if jsonArgIndex < len(activityArgs) {
			if err := json.Unmarshal(activityArgs[jsonArgIndex], argPtr.Interface()); err != nil {
				return nil, fmt.Errorf("failed to unmarshal argument %d: %w", jsonArgIndex, err)
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

type historyIterator interface {
	HasNext() bool
	Next() (*historypb.HistoryEvent, error)
}

type activityInvocation struct {
	Name string
	Args []json.RawMessage
}

func findActivityInvocation(iter historyIterator, dataConverter converter.DataConverter, registry map[string]interface{}, identifier string) (activityInvocation, error) {
	scheduledActivities := make(map[int64]*historypb.ActivityTaskScheduledEventAttributes)
	var matchedActivity *historypb.ActivityTaskScheduledEventAttributes
	var matchedActivityScheduledEventID int64
	var matchedEvent *historypb.ActivityTaskScheduledEventAttributes
	var matchedEventScheduledEventID int64

	eventID, parseErr := strconv.ParseInt(identifier, 10, 64)
	identifierIsEventID := parseErr == nil

	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return activityInvocation{}, fmt.Errorf("error fetching workflow history: %w", err)
		}

		switch event.EventType {
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs == nil {
				continue
			}

			scheduledActivities[event.EventId] = attrs
			if identifier == attrs.ActivityId && matchedActivity == nil {
				matchedActivity = attrs
				matchedActivityScheduledEventID = event.EventId
			}
			if identifierIsEventID && eventID == event.EventId {
				matchedEvent = attrs
				matchedEventScheduledEventID = event.EventId
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_STARTED:
			if !identifierIsEventID || eventID != event.EventId {
				continue
			}
			attrs := event.GetActivityTaskStartedEventAttributes()
			if attrs != nil {
				matchedEvent = scheduledActivities[attrs.ScheduledEventId]
				matchedEventScheduledEventID = attrs.ScheduledEventId
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if !identifierIsEventID || eventID != event.EventId {
				continue
			}
			attrs := event.GetActivityTaskCompletedEventAttributes()
			if attrs != nil {
				matchedEvent = scheduledActivities[attrs.ScheduledEventId]
				matchedEventScheduledEventID = attrs.ScheduledEventId
			}
		}
	}

	matched := matchedEvent
	matchedScheduledEventID := matchedEventScheduledEventID
	if matchedActivity != nil {
		matched = matchedActivity
		matchedScheduledEventID = matchedActivityScheduledEventID
	}
	if matched == nil {
		return activityInvocation{}, fmt.Errorf("activity %q not found in workflow history", identifier)
	}

	activityName := matched.ActivityType.GetName()
	activityFn, ok := registry[activityName]
	if !ok {
		return activityInvocation{}, fmt.Errorf("activity %q not found in registry", activityName)
	}

	invocation, err := decodeActivityInvocation(dataConverter, activityName, matched.Input, activityFn)
	if err != nil {
		return activityInvocation{}, fmt.Errorf("decode activity %q at scheduled event %d: %w", matched.ActivityId, matchedScheduledEventID, err)
	}
	return invocation, nil
}

func decodeActivityInvocation(
	dataConverter converter.DataConverter,
	activityName string,
	payloads interface{ GetPayloads() []*commonpb.Payload },
	activityFn interface{},
) (activityInvocation, error) {
	invocation := activityInvocation{Name: activityName}
	if payloads == nil {
		return invocation, nil
	}

	fnType := reflect.TypeOf(activityFn)
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	firstActivityArg := 0
	if fnType.NumIn() > 0 && fnType.In(0) == contextType {
		firstActivityArg = 1
	}

	inputPayloads := payloads.GetPayloads()
	expectedArgs := fnType.NumIn() - firstActivityArg
	if len(inputPayloads) != expectedArgs {
		return activityInvocation{}, fmt.Errorf("activity expects %d arguments but history contains %d", expectedArgs, len(inputPayloads))
	}

	for i, payload := range inputPayloads {
		argType := fnType.In(firstActivityArg + i)
		argPtr := reflect.New(argType)
		if err := dataConverter.FromPayload(payload, argPtr.Interface()); err != nil {
			return activityInvocation{}, fmt.Errorf("decode argument %d as %s: %w", i, argType, err)
		}
		argument, err := json.Marshal(argPtr.Elem().Interface())
		if err != nil {
			return activityInvocation{}, fmt.Errorf("marshal argument %d: %w", i, err)
		}
		invocation.Args = append(invocation.Args, argument)
	}

	return invocation, nil
}

func loadActivityInvocation(ctx context.Context, flowID string, identifier string) (activityInvocation, error) {
	service, err := sidekick.GetService()
	if err != nil {
		return activityInvocation{}, fmt.Errorf("error initializing storage: %w", err)
	}

	clientOptions, err := common.NewTemporalClientOptions(service, common.GetTemporalServerHostPort())
	if err != nil {
		return activityInvocation{}, fmt.Errorf("error creating Temporal client options: %w", err)
	}

	temporalClient, err := client.Dial(clientOptions)
	if err != nil {
		return activityInvocation{}, fmt.Errorf("error connecting to Temporal: %w", err)
	}
	defer temporalClient.Close()

	iter := temporalClient.GetWorkflowHistory(ctx, flowID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	return findActivityInvocation(iter, clientOptions.DataConverter, buildActivityRegistry(), identifier)
}

func loadJSONInvocation(activityName string, path string, readFile func(string) ([]byte, error)) (activityInvocation, error) {
	inputBytes, err := readFile(path)
	if err != nil {
		return activityInvocation{}, fmt.Errorf("read input file: %w", err)
	}

	var activityArgs []json.RawMessage
	if err := json.Unmarshal(inputBytes, &activityArgs); err != nil {
		return activityInvocation{}, fmt.Errorf("parse input JSON: %w", err)
	}

	return activityInvocation{
		Name: activityName,
		Args: activityArgs,
	}, nil
}

func resolveActivityInvocation(
	ctx context.Context,
	args []string,
	loadFromHistory func(context.Context, string, string) (activityInvocation, error),
	readFile func(string) ([]byte, error),
	stat func(string) (os.FileInfo, error),
) (activityInvocation, error) {
	if len(args) == 3 && args[0] == "json" {
		return loadJSONInvocation(args[1], args[2], readFile)
	}
	if len(args) != 2 {
		return activityInvocation{}, fmt.Errorf("invalid arguments")
	}

	if fileInfo, err := stat(args[1]); err == nil && fileInfo.Mode().IsRegular() {
		return loadJSONInvocation(args[0], args[1], readFile)
	}

	return loadFromHistory(ctx, args[0], args[1])
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var timeout time.Duration
	var direct bool
	flag.DurationVar(&timeout, "timeout", 180*time.Second, "Timeout for the activity execution")
	flag.BoolVar(&direct, "direct", true, "Execute activity directly without Temporal workflow")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  %s [--timeout duration] [--direct] <flow-id> <activity-id-or-scheduled-started-completed-event-id>\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  %s [--timeout duration] [--direct] json <activity-name> <json-file-path>\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  %s [--timeout duration] [--direct] <activity-name> <existing-json-file-path>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if (len(args) != 2 && len(args) != 3) || (len(args) == 3 && args[0] != "json") {
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	invocation, err := resolveActivityInvocation(ctx, args, loadActivityInvocation, os.ReadFile, os.Stat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading activity: %v\n", err)
		os.Exit(1)
	}
	activityName := invocation.Name
	activityArgs := invocation.Args

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
	service, err := sidekick.GetService()
	if err != nil {
		return nil, fmt.Errorf("error initializing storage: %w", err)
	}
	clientOptions, err := common.NewTemporalClientOptions(service, hostPort)
	if err != nil {
		return nil, fmt.Errorf("error creating Temporal client options: %w", err)
	}
	temporalClient, err := client.Dial(clientOptions)
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
