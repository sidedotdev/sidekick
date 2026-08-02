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
func appendWebSearchToolIfNonLocal(dCtx DevContext, tools []*llm.Tool) []*llm.Tool {
	v := workflow.GetVersion(dCtx, "web-search-tool", workflow.DefaultVersion, 1)
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
	return append(tools, &llm.Tool{Type: common.ToolTypeWebSearch})
}