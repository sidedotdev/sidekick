package dev

import (
	"fmt"
	"testing"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/assert"
)

func advisorTestMessage(role llm2.Role, text, contextType string) llm2.Message {
	block := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: text}
	if contextType != "" {
		persisted_ai.SetContextType(&block, contextType)
	}
	return llm2.Message{Role: role, Content: []llm2.ContentBlock{block}}
}

func TestRenderExecutorTranscriptRetainsUserFeedbackOutsideRecentWindow(t *testing.T) {
	t.Parallel()

	msgs := []common.Message{
		advisorTestMessage(llm2.RoleUser, "explain the verify step", persisted_ai.ContextTypeInitialInstructions),
		advisorTestMessage(llm2.RoleAssistant, "here is how verify works", ""),
		advisorTestMessage(llm2.RoleUser, "go ahead and implement.", persisted_ai.ContextTypeUserFeedback),
	}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, advisorTestMessage(llm2.RoleAssistant, fmt.Sprintf("filler step %d", i), ""))
	}

	tests := []struct {
		name   string
		render func([]common.Message, int) string
	}{
		{name: "full", render: renderExecutorTranscript},
		{name: "recent", render: renderExecutorRecentTranscript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			transcript := tt.render(msgs, 20)
			assert.Contains(t, transcript, "go ahead and implement.")
			assert.Contains(t, transcript, "filler step 29")
			assert.NotContains(t, transcript, "filler step 0\n")
			assert.NotContains(t, transcript, "here is how verify works")
		})
	}
}

func TestRenderExecutorTranscriptDoesNotDuplicateRecentUserFeedback(t *testing.T) {
	t.Parallel()

	msgs := []common.Message{
		advisorTestMessage(llm2.RoleAssistant, "working", ""),
		advisorTestMessage(llm2.RoleUser, "go ahead and implement.", persisted_ai.ContextTypeUserFeedback),
	}
	transcript := renderExecutorTranscript(msgs, 20)
	assert.Equal(t, "[assistant] working\n[user] go ahead and implement.\n", transcript)
}
