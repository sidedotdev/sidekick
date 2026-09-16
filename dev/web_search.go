package dev

import (
	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"

	"go.temporal.io/sdk/workflow"
)

// appendWebSearchToolIfNonLocal adds the provider-native web search tool for
// agent loops that benefit from live web results. It is enabled only when the
// flow's execution environment is non-local: local environments run against
// the user's machine where unexpected network access is undesirable by
// default, while remote environments (e.g. devpod) are sandboxed.
func appendWebSearchToolIfNonLocal(dCtx DevContext, tools []*llm.Tool, modelConfig common.ModelConfig) []*llm.Tool {
	v := workflow.GetVersion(dCtx, "web-search-tool", workflow.DefaultVersion, 2)
	if v < 1 {
		return tools
	}
	if dCtx.EnvContainer == nil || dCtx.EnvContainer.Env == nil {
		return tools
	}
	switch dCtx.EnvContainer.Env.GetType() {
	case env.EnvTypeLocal, env.EnvTypeLocalGitWorktree:
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
