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
		if message, ok := m.(llm2.Message); ok {
			for _, block := range message.Content {
				if block.Reasoning == nil {
					continue
				}
				summary := strings.TrimSpace(block.Reasoning.Summary)
				if summary != "" {
					b.WriteString(fmt.Sprintf("[%s] thinking_summary %s\n", m.GetRole(), summary))
				}
			}
		}
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

// SeedAdvisorRequirementsInput is the activity input for extracting the
// executor's initial-instructions (requirements) message and persisting it as a
// standalone block for the advisor history.
type SeedAdvisorRequirementsInput struct {
	ExecutorHistory *persisted_ai.ChatHistoryContainer `json:"executorHistory"`
	Provider        string                             `json:"provider"`
	FlowId          string                             `json:"flowId"`
	WorkspaceId     string                             `json:"workspaceId"`
}

// SeedAdvisorRequirementsOutput carries the persisted requirements ref and its
// length, measured with the same accounting used by the retention logic. Ref is
// nil when the executor history has no initial-instructions message.
type SeedAdvisorRequirementsOutput struct {
	Ref    *persisted_ai.MessageRef `json:"ref"`
	Length int                      `json:"length"`
}

// SeedAdvisorRequirementsActivity hydrates the executor chat history, locates
// the message whose content is marked ContextTypeInitialInstructions, persists
// it as a standalone block (preserving the marker), and returns a ref plus the
// message length. Hydration happens inside the activity because the in-workflow
// chat history is refs-only (not hydrated).
func (a *AdvisorActivities) SeedAdvisorRequirementsActivity(ctx context.Context, input SeedAdvisorRequirementsInput) (*SeedAdvisorRequirementsOutput, error) {
	if input.ExecutorHistory == nil {
		return &SeedAdvisorRequirementsOutput{}, nil
	}
	if err := input.ExecutorHistory.Hydrate(ctx, a.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate executor history: %w", err)
	}

	for _, msg := range input.ExecutorHistory.Llm2Messages() {
		if llm2MessageContextType(msg) != persisted_ai.ContextTypeInitialInstructions {
			continue
		}
		cha := &persisted_ai.ChatHistoryActivities{Storage: a.Storage}
		ref, err := cha.AppendMessage(ctx, persisted_ai.AppendMessageInput{
			FlowId:      input.FlowId,
			WorkspaceId: input.WorkspaceId,
			Message:     msg,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to persist advisor requirements: %w", err)
		}
		return &SeedAdvisorRequirementsOutput{
			Ref:    ref,
			Length: persisted_ai.Llm2MessageLength(input.Provider, msg),
		}, nil
	}
	return &SeedAdvisorRequirementsOutput{}, nil
}

// llm2MessageContextType returns the context type of the first content block
// that carries one, matching the accounting used by the retention logic.
func llm2MessageContextType(msg llm2.Message) string {
	for _, block := range msg.Content {
		if ct := persisted_ai.GetContextType(block); ct != "" {
			return ct
		}
	}
	return ""
}
