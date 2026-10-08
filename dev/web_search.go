package dev

import (
	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"

	"go.temporal.io/sdk/workflow"
)

const webSearchToolMaxVersion workflow.Version = 3

// appendWebSearchTool adds the provider-native web search tool for agent loops
// that benefit from live web results, when the selected provider supports it.
func appendWebSearchTool(dCtx DevContext, tools []*llm.Tool, modelConfig common.ModelConfig) []*llm.Tool {
	v := workflow.GetVersion(dCtx, "web-search-tool", workflow.DefaultVersion, webSearchToolMaxVersion)
	if v < 1 {
		return tools
	}
	if v < 3 && !isNonLocalEnv(dCtx) {
		return tools
	}
	if v >= 2 && !providerSupportsWebSearch(modelConfig, dCtx.GetProviders()) {
		return tools
	}
	return append(tools, &llm.Tool{Type: common.ToolTypeWebSearch})
}

// isNonLocalEnv preserves the pre-v3 restriction of web search to remote
// environments for replaying workflows started before that restriction was lifted.
func isNonLocalEnv(dCtx DevContext) bool {
	if dCtx.EnvContainer == nil || dCtx.EnvContainer.Env == nil {
		return false
	}
	switch dCtx.EnvContainer.Env.GetType() {
	case env.EnvTypeLocal, env.EnvTypeLocalGitWorktree:
		return false
	}
	return true
}

func providerSupportsWebSearch(modelConfig common.ModelConfig, providers []common.ModelProviderPublicConfig) bool {
	providerType := modelConfig.Provider
	var builtinTools []string
	for _, provider := range providers {
		if provider.Name == modelConfig.Provider {
			providerType = provider.Type
			builtinTools = provider.BuiltinTools
			break
		}
	}

	switch providerType {
	case "openai", "anthropic", "google":
		return true
	case "openai_responses_compatible", "anthropic_compatible":
		for _, tool := range builtinTools {
			if tool == "web_search" {
				return true
			}
		}
	}
	return false
}
