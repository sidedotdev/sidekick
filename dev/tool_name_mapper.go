package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
)

type anthropicToolNameMapper struct{}

var anthropicToolNameMap map[string]string
var anthropicReverseToolNameMap map[string]string

func init() {
	anthropicToolNameMap = map[string]string{
		bulkReadFileTool.Name:         "Read",
		bulkSearchRepositoryTool.Name: "Grep",
		runCommandTool.Name:           "Bash",
		getHelpOrInputTool.Name:       "AskUserQuestion",
		getSymbolDefinitionsTool.Name: "mcp__tu__get_symbol_definitions",
		doneTool.Name:                 "Done",
	}

	anthropicReverseToolNameMap = make(map[string]string, len(anthropicToolNameMap))
	for sidekickName, oauthName := range anthropicToolNameMap {
		anthropicReverseToolNameMap[oauthName] = sidekickName
	}
}

func (anthropicToolNameMapper) MapToolName(name string) string {
	if mappedName, ok := anthropicToolNameMap[name]; ok {
		return mappedName
	}
	return name
}

func (anthropicToolNameMapper) ReverseMapToolName(name string) string {
	if mappedName, ok := anthropicReverseToolNameMap[name]; ok {
		return mappedName
	}
	return name
}

func resolveStreamToolNameMapper(modelConfig common.ModelConfig, secrets secret_manager.SecretManagerContainer) (*persisted_ai.ToolNameMapper, error) {
	if modelConfig.NormalizedProviderName() != "anthropic" {
		return nil, nil
	}

	_, useOAuth, err := llm.GetAnthropicOAuthCredentials(secrets.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	if !useOAuth {
		return nil, nil
	}

	var mapper persisted_ai.ToolNameMapper = anthropicToolNameMapper{}
	return &mapper, nil
}
