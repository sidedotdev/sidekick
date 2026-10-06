package dev

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"time"

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
	// AuthType is the flow's auth type override for the provider, if any. API
	// key auth never uses the OAuth tool name mapping.
	AuthType common.ProviderAuthType
	Secrets  secret_manager.SecretManagerContainer
}

type ResolveToolNameMappingResult struct {
	UseMapping bool
}

func ResolveToolNameMappingActivity(_ context.Context, input ResolveToolNameMappingInput) (ResolveToolNameMappingResult, error) {
	if common.NormalizeProviderAuthType(string(input.AuthType)) == common.ProviderAuthTypeAPI {
		return ResolveToolNameMappingResult{}, nil
	}
	_, useOAuth, err := llm.GetAnthropicOAuthCredentials(input.Secrets.SecretManager)
	if err != nil {
		// Wrapping would hide the non-retryable type and details from Temporal.
		if _, ok := common.AsProviderAuthError(err); ok {
			return ResolveToolNameMappingResult{}, err
		}
		return ResolveToolNameMappingResult{}, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	return ResolveToolNameMappingResult{UseMapping: useOAuth}, nil
}

func resolveStreamToolNameMapping(eCtx flow_action.ExecContext, modelConfig common.ModelConfig, secrets secret_manager.SecretManagerContainer) (*persisted_ai.ToolNameMappingConfig, error) {
	// FIXME use provider type instead (needs list of providers or the specific provider type passed in)
	if modelConfig.NormalizedProviderName() != "ANTHROPIC" {
		return nil, nil
	}

	v := workflow.GetVersion(eCtx, "resolve-tool-name-mapping-activity", workflow.DefaultVersion, 1)
	if v >= 1 {
		var result ResolveToolNameMappingResult
		retryECtx := eCtx
		retryECtx.Context = utils.RetryCtx(eCtx.Context, 3, 10*time.Second)
		var err error
		if workflow.GetVersion(eCtx, "provider-auth-user-retry", workflow.DefaultVersion, 1) >= 1 {
			err = flow_action.PerformWithUserRetryOptions(
				flow_action.ActionContext{ExecContext: retryECtx, ActionType: "Resolve tool name mapping"},
				flow_action.UserRetryOptions{
					OnFailure: persisted_ai.ProviderAuthFailureHandler{},
					ResolveArgs: func() ([]interface{}, error) {
						authType, _ := retryECtx.ProviderAuthTypeOverride(modelConfig.Provider)
						return []interface{}{ResolveToolNameMappingInput{Secrets: secrets, AuthType: authType}}, nil
					},
				},
				ResolveToolNameMappingActivity,
				&result,
			)
		} else {
			err = flow_action.PerformActivityWithUserRetry(
				retryECtx,
				"Resolve tool name mapping",
				ResolveToolNameMappingActivity,
				&result,
				ResolveToolNameMappingInput{Secrets: secrets},
			)
		}
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
