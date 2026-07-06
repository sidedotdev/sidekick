package dev

import (
	"context"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
)

// AdvisorActivities holds dependencies for advisor-specific activities.
type AdvisorActivities struct {
	Storage common.KeyValueStorage
}

// SummarizeExecutorHistoryInput is the activity input for rendering the advisor
// turn prompt from the executor's chat history and persisting it to KV.
type SummarizeExecutorHistoryInput struct {
	ExecutorHistory   *persisted_ai.ChatHistoryContainer `json:"executorHistory"`
	MaxRecentMessages int                                `json:"maxRecentMessages"`
	FlowId            string                             `json:"flowId"`
	WorkspaceId       string                             `json:"workspaceId"`
}

// SummarizeExecutorHistoryActivity hydrates the executor chat history, renders
// the advisor turn prompt from its trailing messages, persists it as a content
// block, and returns a ref to that block. Hydration happens inside the activity
// because the in-workflow chat history is refs-only (not hydrated).
func (a *AdvisorActivities) SummarizeExecutorHistoryActivity(ctx context.Context, input SummarizeExecutorHistoryInput) (*persisted_ai.MessageRef, error) {
	transcript := "(no executor history yet)"
	if input.ExecutorHistory != nil {
		if err := input.ExecutorHistory.Hydrate(ctx, a.Storage); err != nil {
			return nil, fmt.Errorf("failed to hydrate executor history: %w", err)
		}
		if rendered := renderExecutorTranscript(input.ExecutorHistory.Messages(), input.MaxRecentMessages); rendered != "" {
			transcript = rendered
		}
	}

	turnPrompt := RenderPrompt(AdvisorTurn, map[string]string{
		"recentHistory": transcript,
	})
	block := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: turnPrompt}
	ref, err := persisted_ai.PersistContentBlock(ctx, a.Storage, input.FlowId, input.WorkspaceId, string(llm2.RoleUser), block)
	if err != nil {
		return nil, fmt.Errorf("failed to persist advisor turn prompt: %w", err)
	}
	return ref, nil
}

// renderExecutorTranscript renders the trailing executor messages into a plain
// text transcript for the advisor prompt, capped at maxRecent when positive.
func renderExecutorTranscript(msgs []common.Message, maxRecent int) string {
	start := 0
	if maxRecent > 0 && len(msgs) > maxRecent {
		start = len(msgs) - maxRecent
	}
	var b strings.Builder
	for _, m := range msgs[start:] {
		content := strings.TrimSpace(m.GetContentString())
		if content != "" {
			b.WriteString(fmt.Sprintf("[%s] %s\n", m.GetRole(), content))
		}
		for _, tc := range m.GetToolCalls() {
			b.WriteString(fmt.Sprintf("[%s] tool_call %s(%s)\n", m.GetRole(), tc.Name, tc.Arguments))
		}
	}
	return b.String()
}
