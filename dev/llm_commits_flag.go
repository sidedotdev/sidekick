package dev

import (
	"sidekick/fflag"
)

// llmCommitMessagesEnabled reports whether Sidekick commits through an LLM
// rather than with title-derived messages. Coding passes, plan steps and the
// merge must agree on who commits, or work could be committed twice or left
// for a merge that no longer commits it, which the flag's once-per-flow
// evaluation guarantees.
func llmCommitMessagesEnabled(dCtx DevContext) bool {
	return fflag.IsEnabledOrDefault(dCtx, fflag.LlmCommitMessages, true)
}
