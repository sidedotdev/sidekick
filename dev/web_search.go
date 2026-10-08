package dev

import (
	"sidekick/common"
	"sidekick/llm"

	"go.temporal.io/sdk/workflow"
)

const webSearchToolMaxVersion workflow.Version = 2

// appendWebSearchTool adds the provider-native web search tool for agent loops
// that benefit from live web results, when the selected provider supports it.
func appendWebSearchTool(dCtx DevContext, tools []*llm.Tool, modelConfig common.ModelConfig) []*llm.Tool {
	v := workflow.GetVersion(dCtx, "web-search-tool", workflow.DefaultVersion, webSearchToolMaxVersion)
	if v < 1 {
		return tools
	}
	if v >= 2 && !providerSupportsWebSearch(modelConfig, dCtx.GetProviders()) {
		return tools
	}
	return append(tools, &llm.Tool{Type: common.ToolTypeWebSearch})
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
