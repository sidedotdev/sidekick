package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"

	"sidekick"
	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	sidekicklogger "sidekick/logger"
	"sidekick/srv"
	"sidekick/telemetry"
	"sidekick/workspace"

	"sidekick/dev"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/poll_failures"
)

// Worker wraps a Temporal worker with telemetry shutdown
type Worker struct {
	worker.Worker
	shutdownTracer func(context.Context) error
}

// Stop stops the worker and shuts down the tracer
func (w *Worker) Stop() {
	w.Worker.Stop()
	if w.shutdownTracer != nil {
		if err := w.shutdownTracer(context.Background()); err != nil {
			log.Error().Err(err).Msg("Failed to shutdown telemetry tracer")
		}
	}
}

// StartWorker initializes and starts a new worker
func StartWorker(hostPort string, taskQueue string) *Worker {
	shutdownTracer, err := telemetry.InitTracer("sidekick-worker")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize telemetry tracer")
	}

	featureFlag, err := fflag.NewFFlag("flags.yml")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create go-feature-flag instance")
	}
	ffa := fflag.FFlagActivities{FFlag: featureFlag}

	logger := logur.LoggerToKV(zerologadapter.New(sidekicklogger.Get()))
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create tracing interceptor")
	}
	clientOptions := client.Options{
		Logger:       logger,
		HostPort:     hostPort,
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	}
	var temporalClient client.Client
	for i := 0; i < 5; i++ {
		temporalClient, err = client.Dial(clientOptions)
		if err == nil {
			break
		}
		log.Debug().Err(err).Msgf("Failed to create Temporal client, retrying in 500ms (attempt %d/5)", i+1)
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create Temporal client after multiple retries.")
	}

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	err = service.CheckConnection(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to storage")
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

	w := worker.New(temporalClient, taskQueue, worker.Options{
		OnFatalError: func(err error) {
			log.Fatal().Err(err).Msg("Worker encountered a fatal error")
		},
	})
	RegisterWorkflows(w)

	w.RegisterActivity(env.NewLocalGitWorktreeActivity)
	w.RegisterActivity(&srv.Activities{Service: service})
	w.RegisterActivity(sidekick.GithubCloneRepoActivity)
	w.RegisterActivity(llmActivities)
	w.RegisterActivity(pollFailuresActivities)
	w.RegisterActivity(lspActivities)
	w.RegisterActivity(treeSitterActivities)
	w.RegisterActivity(codingActivities)
	w.RegisterActivity(ragActivities)
	w.RegisterActivity(env.EnvRunCommandActivity)
	w.RegisterActivity(git.GitDiffActivity)
	w.RegisterActivity(git.GitAddActivity)
	w.RegisterActivity(git.GitRestoreActivity)
	w.RegisterActivity(git.GitCommitActivity)
	w.RegisterActivity(git.GitCheckoutActivity)
	w.RegisterActivity(git.GitMergeActivity)
	w.RegisterActivity(git.ListWorktreesActivity)
	w.RegisterActivity(git.CleanupWorktreeActivity)
	w.RegisterActivity(git.GetCurrentBranch)
	w.RegisterActivity(git.GetDefaultBranch)
	w.RegisterActivity(git.ListLocalBranches)
	w.RegisterActivity(embedActivities)
	w.RegisterActivity(vectorActivities)
	w.RegisterActivity(flowActivities)

	w.RegisterActivity(dev.GetRepoConfigActivity)
	w.RegisterActivity(dev.GetSymbolsActivity)
	w.RegisterActivity(devManagerActivities)
	w.RegisterActivity(dev.ApplyEditBlocksActivity) // backcompat for <= v0.4.2
	w.RegisterActivity(devActivities)
	w.RegisterActivity(dev.ReadFileActivity)
	w.RegisterActivity(dev.BulkReadFileActivity)
	w.RegisterActivity(dev.ManageChatHistoryActivity)
	w.RegisterActivity(dev.ManageChatHistoryV2Activity)
	w.RegisterActivity(ffa.EvalBoolFlag)
	w.RegisterActivity(common.GetLocalConfig)
	w.RegisterActivity(common.BaseCommandPermissionsActivity)

	w.RegisterActivity(&workspace.Activities{Storage: service})

	err = w.Start()
	if err != nil {
		log.Fatal().Err(err)
	}

	return &Worker{
		Worker:         w,
		shutdownTracer: shutdownTracer,
	}
}

func RegisterWorkflows(w worker.WorkflowRegistry) {
	w.RegisterWorkflow(persisted_ai.TestOpenAiEmbedActivityWorkflow)
	w.RegisterWorkflow(dev.DevAgentManagerWorkflow)
	w.RegisterWorkflow(dev.PlannedDevWorkflow)
	w.RegisterWorkflow(dev.BasicDevWorkflow)
	w.RegisterWorkflow(poll_failures.PollFailuresWorkflow)
}
