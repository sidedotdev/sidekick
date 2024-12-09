package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"

	"sidekick"
	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/db"
	"sidekick/workspace"

	"sidekick/dev"
	"sidekick/embedding"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/poll_failures"
)

// TODO move configuring the worker to a separate function to be able to test it
func StartWorker(hostPort string, taskQueue string) {
	featureFlag, err := fflag.NewFFlag("flags.yml")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create go-feature-flag instance")
	}
	ffa := fflag.FFlagActivities{FFlag: featureFlag}

	logger := logur.LoggerToKV(zerologadapter.New(log.Logger))
	clientOptions := client.Options{
		Logger:   logger,
		HostPort: hostPort,
	}
	c, err := client.Dial(clientOptions)

	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create Temporal client.")
	}

	defer c.Close()

	redisDb := db.NewRedisDatabase()
	_, err = redisDb.Client.Ping(context.Background()).Result()
	if err != nil {
		log.Fatal().Err(err)
	}

	devManagerActivities := &dev.DevAgentManagerActivities{
		DatabaseAccessor: redisDb,
		TemporalClient:   c,
	}
	//redisDb := db.RedisDatabase{Client: redisClient}
	flowActivities := &flow_action.FlowActivities{DatabaseAccessor: redisDb}
	openAIActivities := &persisted_ai.OpenAIActivities{
		DatabaseAccessor: redisDb,
		Embedder:         embedding.OpenAIEmbedder{},
	}
	llmActivities := &persisted_ai.LlmActivities{
		FlowEventAccessor: &db.RedisFlowEventAccessor{Client: redisDb.Client},
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
		DatabaseAccessor: redisDb,
	}
	codingActivities := &coding.CodingActivities{
		TreeSitterActivities: treeSitterActivities,
		LSPActivities:        lspActivities,
	}
	vectorActivities := &embedding.VectorActivities{
		DatabaseAccessor: redisDb,
	}
	ragActivities := &persisted_ai.RagActivities{
		DatabaseAccessor: redisDb,
		Embedder:         embedding.OpenAIEmbedder{},
	}

	pollFailuresActivities := &poll_failures.PollFailuresActivities{
		TemporalClient:   c,
		DatabaseAccessor: redisDb,
	}

	w := worker.New(c, taskQueue, worker.Options{})
	RegisterWorkflows(w)

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
	w.RegisterActivity(openAIActivities)
	w.RegisterActivity(vectorActivities)
	w.RegisterActivity(flowActivities)

	w.RegisterActivity(dev.GetRepoConfigActivity)
	w.RegisterActivity(devManagerActivities)
	w.RegisterActivity(dev.ApplyEditBlocksActivity)
	w.RegisterActivity(dev.ReadFileActivity)
	w.RegisterActivity(dev.ManageChatHistoryActivity)
	w.RegisterActivity(ffa.EvalBoolFlag)

	workspaceActivities := &workspace.Activities{
		DatabaseAccessor: redisDb,
	}
	w.RegisterActivity(workspaceActivities.GetWorkspaceConfig)

	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Warn().Err(err)
	}
}

func RegisterWorkflows(w worker.WorkflowRegistry) {
	w.RegisterWorkflow(sidekick.ExampleLlmActivitiesWorkflow)
	w.RegisterWorkflow(persisted_ai.TestOpenAiEmbedActivityWorkflow)
	w.RegisterWorkflow(dev.DevAgentManagerWorkflow)
	w.RegisterWorkflow(dev.PlannedDevWorkflow)
	w.RegisterWorkflow(dev.BasicDevWorkflow)
	w.RegisterWorkflow(poll_failures.PollFailuresWorkflow)
}
