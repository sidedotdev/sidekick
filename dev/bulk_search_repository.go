package dev

import (
	"context"
	"fmt"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/repotools"
	"sidekick/sideagent"
	"sidekick/utils"
	"time"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var bulkSearchRepositoryTool = llm.Tool{
	Name:        "bulk_search_repository",
	Description: "Used to perform multiple searches within the repository, each for files matching a given glob pattern and containing a search term.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&BulkSearchRepositoryParams{}),
}

type BulkSearchRepositoryParams = repotools.BulkSearchRepositoryParams

type BulkSearchRepositoryActivityInput struct {
	EnvContainer env.EnvContainer           `json:"envContainer"`
	Params       BulkSearchRepositoryParams `json:"params"`
}

type BulkSearchRepositoryActivityOutput struct {
	Result string `json:"result"`
}

// BulkSearchRepositoryActivity performs all the searches within a single
// activity, so that the many intermediate command inputs and outputs never
// cross the activity boundary.
func BulkSearchRepositoryActivity(ctx context.Context, input BulkSearchRepositoryActivityInput) (BulkSearchRepositoryActivityOutput, error) {
	if provider, ok := input.EnvContainer.Env.(env.AgentProvider); ok {
		var output BulkSearchRepositoryActivityOutput
		err := provider.WithAgent(ctx, func(client *sideagent.Client) error {
			var err error
			output.Result, err = client.BulkSearchRepository(ctx, input.EnvContainer.Env.GetWorkingDirectory(), input.Params)
			return err
		})
		return output, err
	}
	result, err := bulkSearchRepository(activitySearchRunner{ctx: ctx}, input.EnvContainer, input.Params)
	if err != nil {
		return BulkSearchRepositoryActivityOutput{}, err
	}
	return BulkSearchRepositoryActivityOutput{Result: result}, nil
}

func BulkSearchRepository(ctx workflow.Context, envContainer env.EnvContainer, bulkSearchRepositoryParams BulkSearchRepositoryParams) (string, error) {
	if len(bulkSearchRepositoryParams.Searches) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	v := workflow.GetVersion(ctx, "bulk-search-single-activity", workflow.DefaultVersion, 1)
	if v >= 1 {
		if workflow.GetVersion(ctx, "bulk-search-activity-retry", workflow.DefaultVersion, 1) >= 1 {
			// Search and its ignore-file setup are idempotent, unlike arbitrary
			// tool commands that inherit the caller's no-retry policy.
			options := workflow.GetActivityOptions(ctx)
			options.RetryPolicy = &temporal.RetryPolicy{
				InitialInterval: 5 * time.Second,
				MaximumAttempts: 2,
			}
			ctx = workflow.WithActivityOptions(ctx, options)
		}
		var output BulkSearchRepositoryActivityOutput
		err := workflow.ExecuteActivity(ctx, BulkSearchRepositoryActivity, BulkSearchRepositoryActivityInput{
			EnvContainer: envContainer,
			Params:       bulkSearchRepositoryParams,
		}).Get(ctx, &output)
		if err != nil {
			return "", err
		}
		return output.Result, nil
	}

	return bulkSearchRepository(workflowSearchRunner{ctx: ctx}, envContainer, bulkSearchRepositoryParams)
}

func bulkSearchRepository(runner searchRunner, container env.EnvContainer, params BulkSearchRepositoryParams) (string, error) {
	return repotools.BulkSearchRepository(repositorySearchRunner{runner, container}, params)
}

func ForceToolBulkSearchRepository(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer) ([]llm.ToolCall, error) {
	actionCtx := dCtx.ExecContext.NewActionContext("generate.repo_search_query")
	modelConfig := dCtx.GetModelConfig(common.CodeLocalizationKey, 0, "default")
	toolNameMapping, err := resolveStreamToolNameMapping(actionCtx.ExecContext, modelConfig, *actionCtx.Secrets)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tool name mapping: %v", err)
	}
	response, err := persisted_ai.ForceToolCallWithTrackOptionsV2(
		actionCtx,
		flow_action.TrackOptions{},
		modelConfig,
		chatHistory,
		toolNameMapping,
		&bulkSearchRepositoryTool,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to force tool call: %v", err)
	}
	return response.GetMessage().GetToolCalls(), nil
}

func (r repositorySearchRunner) FileSymbols(path string) ([]tree_sitter.Symbol, error) {
	if runner, ok := r.searchRunner.(workflowSearchRunner); ok {
		var symbols []tree_sitter.Symbol
		err := workflow.ExecuteActivity(runner.ctx, GetSymbolsActivity, r.container, path).Get(runner.ctx, &symbols)
		return symbols, err
	}
	return getFileSymbols(r.searchRunner.(activitySearchRunner).ctx, r.container, path)
}

// getSymbolsMessage returns a message about available symbols in a file if it exists
func GetSymbolsActivity(envContainer env.EnvContainer, filePath string) ([]tree_sitter.Symbol, error) {
	return getFileSymbols(context.Background(), envContainer, filePath)
}

func getFileSymbols(ctx context.Context, envContainer env.EnvContainer, filePath string) ([]tree_sitter.Symbol, error) {
	fileBytes, err := envContainer.Env.ReadFile(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	langName := utils.InferLanguageNameFromFilePath(filePath)
	return tree_sitter.GetFileSymbolsFromBytes(filePath, langName, fileBytes)
}
