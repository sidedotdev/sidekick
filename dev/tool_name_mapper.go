package dev

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

const anthropicMCPToolNamePrefix = "mcp__tu__"

var anthropicToolNameMapping *persisted_ai.ToolNameMappingConfig

func init() {
	forward := map[string]string{
		bulkReadFileTool.Name:         "Read",
		bulkSearchRepositoryTool.Name: "Grep",
		runCommandTool.Name:           "Bash",
		getHelpOrInputTool.Name:       "AskUserQuestion",
	}

	reverse := make(map[string]string, len(forward))
	for sidekickName, mappedName := range forward {
		reverse[mappedName] = sidekickName
	}

	anthropicToolNameMapping = &persisted_ai.ToolNameMappingConfig{
		Forward: forward,
		Reverse: reverse,
		Prefix:  anthropicMCPToolNamePrefix,
	}
}

type ResolveToolNameMappingInput struct {
	Secrets secret_manager.SecretManagerContainer
}

type ResolveToolNameMappingResult struct {
	UseMapping bool
}

func ResolveToolNameMappingActivity(_ context.Context, input ResolveToolNameMappingInput) (ResolveToolNameMappingResult, error) {
	_, useOAuth, err := llm.GetAnthropicOAuthCredentials(input.Secrets.SecretManager)
	if err != nil {
		return ResolveToolNameMappingResult{}, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	return ResolveToolNameMappingResult{UseMapping: useOAuth}, nil
}

func resolveStreamToolNameMapping(ctx workflow.Context, modelConfig common.ModelConfig, secrets secret_manager.SecretManagerContainer) (*persisted_ai.ToolNameMappingConfig, error) {
	// FIXME use provider type instead (needs list of providers or the specific provider type passed in)
	if modelConfig.NormalizedProviderName() != "ANTHROPIC" {
		return nil, nil
	}

	v := workflow.GetVersion(ctx, "resolve-tool-name-mapping-activity", workflow.DefaultVersion, 1)
	if v >= 1 {
		var result ResolveToolNameMappingResult
		err := workflow.ExecuteActivity(
			utils.NoRetryCtx(ctx),
			ResolveToolNameMappingActivity,
			ResolveToolNameMappingInput{Secrets: secrets},
		).Get(ctx, &result)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve tool name mapping: %w", err)
		}
		if !result.UseMapping {
			return nil, nil
		}
		return anthropicToolNameMapping, nil
	}

	_, useOAuth, err := llm.GetAnthropicOAuthCredentials(secrets.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	if !useOAuth {
		return nil, nil
	}
	return anthropicToolNameMapping, nil
}
