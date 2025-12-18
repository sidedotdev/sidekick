package llm2

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"sidekick/common"
)

func TestMessage_ImplementsCommonMessage(t *testing.T) {
	var _ common.Message = &Message{}
}

func TestMessage_GetRole(t *testing.T) {
	tests := []struct {
		name     string
		role     Role
		expected string
	}{
		{"user role", RoleUser, "user"},
		{"assistant role", RoleAssistant, "assistant"},
		{"system role", RoleSystem, "system"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{Role: tt.role}
			assert.Equal(t, tt.expected, msg.GetRole())
		})
	}
}

func TestMessage_GetContentString(t *testing.T) {
	tests := []struct {
		name     string
		content  []ContentBlock
		expected string
	}{
		{
			name:     "empty content",
			content:  []ContentBlock{},
			expected: "",
		},
		{
			name: "single text block",
			content: []ContentBlock{
				{Type: ContentBlockTypeText, Text: "Hello, world!"},
			},
			expected: "Hello, world!",
		},
		{
			name: "multiple text blocks",
			content: []ContentBlock{
				{Type: ContentBlockTypeText, Text: "Hello, "},
				{Type: ContentBlockTypeText, Text: "world!"},
			},
			expected: "Hello, world!",
		},
		{
			name: "mixed block types - only text extracted",
			content: []ContentBlock{
				{Type: ContentBlockTypeText, Text: "Start "},
				{Type: ContentBlockTypeToolUse, ToolUse: &ToolUseBlock{Id: "123", Name: "test"}},
				{Type: ContentBlockTypeText, Text: "End"},
			},
			expected: "Start End",
		},
		{
			name: "no text blocks",
			content: []ContentBlock{
				{Type: ContentBlockTypeToolUse, ToolUse: &ToolUseBlock{Id: "123", Name: "test"}},
				{Type: ContentBlockTypeImage, Image: &ImageRef{}},
			},
			expected: "",
		},
		{
			name: "text blocks with reasoning blocks",
			content: []ContentBlock{
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "thinking..."}},
				{Type: ContentBlockTypeText, Text: "The answer is 42"},
			},
			expected: "The answer is 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{Content: tt.content}
			assert.Equal(t, tt.expected, msg.GetContentString())
		})
	}
}
