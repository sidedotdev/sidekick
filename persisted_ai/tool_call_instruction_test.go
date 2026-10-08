package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendToolCallInstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		choice   common.ToolChoice
		wantText string
	}{
		{
			name:     "specific tool",
			choice:   common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "record_plan"},
			wantText: "You MUST respond by calling the record_plan tool.",
		},
		{
			name:     "required",
			choice:   common.ToolChoice{Type: common.ToolChoiceTypeRequired},
			wantText: "You MUST respond by calling one of the provided tools.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backing := make([]llm2.Message, 1, 4)
			backing[0] = llm2.Message{
				Role:    llm2.RoleUser,
				Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "hi"}},
			}

			got := appendToolCallInstruction(backing, tt.choice)

			require.Len(t, got, 2)
			assert.Equal(t, backing[0], got[0])
			assert.Equal(t, llm2.RoleSystem, got[1].Role)
			require.Len(t, got[1].Content, 1)
			assert.Equal(t, llm2.ContentBlockTypeText, got[1].Content[0].Type)
			assert.Equal(t, tt.wantText, got[1].Content[0].Text)

			assert.Len(t, backing, 1)
			assert.Empty(t, backing[:2][1].Content, "history backing array must not be mutated")
		})
	}
}
