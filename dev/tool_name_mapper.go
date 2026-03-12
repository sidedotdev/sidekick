package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
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

func resolveStreamToolNameMapping(modelConfig common.ModelConfig, secrets secret_manager.SecretManagerContainer) (*persisted_ai.ToolNameMappingConfig, error) {
	// FIXME use provider type instead (needs list of providers or the specific provider type passed in)
	if modelConfig.NormalizedProviderName() != "ANTHROPIC" {
		return nil, nil
	}

	// FIXME oauth refresh means this is fallible, thus shouldn't be in workflow code
	_, useOAuth, err := llm.GetAnthropicOAuthCredentials(secrets.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	if !useOAuth {
		return nil, nil
	}

	return anthropicToolNameMapping, nil
}
