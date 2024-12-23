package llm

import (
	"sidekick/common"
	"testing"

	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
	"github.com/stretchr/testify/assert"
)

func TestAnthropicToChatMessageDelta(t *testing.T) {
	tests := []struct {
		name     string
		input    anthropic.MessageStreamDelta
		expected ChatMessageDelta
		panics   bool
	}{
		{
			name: "text_delta type",
			input: anthropic.MessageStreamDelta{
				Type: "text_delta",
				Text: "Hello, world!",
			},
			expected: ChatMessageDelta{
				Role:    ChatMessageRoleAssistant,
				Content: "Hello, world!",
			},
		},
		{
			name: "input_json_delta type",
			input: anthropic.MessageStreamDelta{
				Type:        "input_json_delta",
				PartialJson: `{"key": "value"}`,
			},
			expected: ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
				ToolCalls: []ToolCall{
					{
						Arguments: `{"key": "value"}`,
					},
				},
			},
		},
		{
			name: "unsupported delta type",
			input: anthropic.MessageStreamDelta{
				Type: "unsupported_type",
			},
			panics: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.panics {
				assert.Panics(t, func() {
					anthropicToChatMessageDelta(tt.input)
				})
			} else {
				result := anthropicToChatMessageDelta(tt.input)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAnthropicToChatMessageResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    anthropic.MessageResponse
		expected *ChatMessageResponse
	}{
		{
			name: "Text content only",
			input: anthropic.MessageResponse{
				Content: []anthropic.MessagePartResponse{
					{Type: "text", Text: "Hello, world!"},
				},
				StopReason:   "stop_sequence",
				StopSequence: "\\n\\nHuman:",
				Usage: anthropic.MessageUsage{
					InputTokens:  10,
					OutputTokens: 20,
				},
				Model: string(anthropic.Claude3Opus),
			},
			expected: &ChatMessageResponse{
				ChatMessage: ChatMessage{
					Role:    ChatMessageRoleAssistant,
					Content: "Hello, world!",
				},
				StopReason:   "stop_sequence",
				StopSequence: "\\n\\nHuman:",
				Usage: Usage{
					InputTokens:  10,
					OutputTokens: 20,
				},
				Model:    string(anthropic.Claude3Opus),
				Provider: common.AnthropicChatProvider,
			},
		},
		{
			name: "Tool call only",
			input: anthropic.MessageResponse{
				Content: []anthropic.MessagePartResponse{
					{
						Type:  "tool_use",
						ID:    "tool1",
						Name:  "search",
						Input: map[string]interface{}{"query": "golang testing"},
					},
				},
				StopReason: "end_turn",
				Usage: anthropic.MessageUsage{
					InputTokens:  5,
					OutputTokens: 15,
				},
				Model: string(anthropic.Claude3Haiku),
			},
			expected: &ChatMessageResponse{
				ChatMessage: ChatMessage{
					Role: ChatMessageRoleAssistant,
					ToolCalls: []ToolCall{
						{
							Id:        "tool1",
							Name:      "search",
							Arguments: `{"query":"golang testing"}`,
						},
					},
				},
				StopReason: "end_turn",
				Usage: Usage{
					InputTokens:  5,
					OutputTokens: 15,
				},
				Model:    string(anthropic.Claude3Haiku),
				Provider: common.AnthropicChatProvider,
			},
		},
		{
			name: "Combination of text and tool call",
			input: anthropic.MessageResponse{
				Content: []anthropic.MessagePartResponse{
					{Type: "text", Text: "Here's the search result:"},
					{
						Type:  "tool_use",
						ID:    "tool2",
						Name:  "search",
						Input: map[string]interface{}{"query": "go unit testing"},
					},
					{Type: "text", Text: "Let me know if you need more information."},
				},
				StopReason: "max_tokens",
				Usage: anthropic.MessageUsage{
					InputTokens:  15,
					OutputTokens: 30,
				},
				Model: string(anthropic.Claude35Sonnet),
			},
			expected: &ChatMessageResponse{
				ChatMessage: ChatMessage{
					Role:    ChatMessageRoleAssistant,
					Content: "Here's the search result:Let me know if you need more information.",
					ToolCalls: []ToolCall{
						{
							Id:        "tool2",
							Name:      "search",
							Arguments: `{"query":"go unit testing"}`,
						},
					},
				},
				StopReason: "max_tokens",
				Usage: Usage{
					InputTokens:  15,
					OutputTokens: 30,
				},
				Model:    string(anthropic.Claude35Sonnet),
				Provider: common.AnthropicChatProvider,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anthropicToChatMessageResponse(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAnthropicFromChatMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    []ChatMessage
		expected []anthropic.MessagePartRequest
	}{
		{
			name: "Simple text messages",
			input: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Hello"},
				{Role: ChatMessageRoleAssistant, Content: "Hi there!"},
			},
			expected: []anthropic.MessagePartRequest{
				{Role: "user", Content: []anthropic.ContentBlock{anthropic.NewTextContentBlock("Hello")}},
				{Role: "assistant", Content: []anthropic.ContentBlock{anthropic.NewTextContentBlock("Hi there!")}},
			},
		},
		{
			name: "Message with tool call",
			input: []ChatMessage{
				{Role: ChatMessageRoleAssistant, Content: "Let me search that for you.", ToolCalls: []ToolCall{
					{Id: "tool1", Name: "search", Arguments: `{"query":"golang testing"}`},
				}},
			},
			expected: []anthropic.MessagePartRequest{
				{
					Role: "assistant",
					Content: []anthropic.ContentBlock{
						anthropic.NewTextContentBlock("Let me search that for you."),
						anthropic.ToolUseContentBlock{
							Type:  "tool_use",
							ID:    "tool1",
							Name:  "search",
							Input: map[string]interface{}{"query": "golang testing"},
						},
					},
				},
			},
		},
		{
			name: "Tool call response",
			input: []ChatMessage{
				{Role: ChatMessageRoleAssistant, Content: "", ToolCalls: []ToolCall{
					{Id: "tool1", Name: "search", Arguments: `{"query":"something"}`},
				}},
				{Role: ChatMessageRoleTool, ToolCallId: "tool1", Content: "results", Name: "search"},
			},
			expected: []anthropic.MessagePartRequest{
				{Role: "assistant", Content: []anthropic.ContentBlock{
					anthropic.ToolUseContentBlock{Type: "tool_use", ID: "tool1", Name: "search", Input: map[string]any{"query": "something"}},
				}},
				{Role: "user", Content: []anthropic.ContentBlock{anthropic.NewToolResultContentBlock("tool1", "results", false)}},
			},
		},
		{
			name: "Consecutive messages from same role",
			input: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Hello"},
				{Role: ChatMessageRoleUser, Content: "How are you?"},
				{Role: ChatMessageRoleAssistant, Content: "Hi!"},
				{Role: ChatMessageRoleAssistant, Content: "I'm doing well, thank you."},
			},
			expected: []anthropic.MessagePartRequest{
				{Role: "user", Content: []anthropic.ContentBlock{
					anthropic.NewTextContentBlock("Hello"),
					anthropic.NewTextContentBlock("How are you?"),
				}},
				{Role: "assistant", Content: []anthropic.ContentBlock{
					anthropic.NewTextContentBlock("Hi!"),
					anthropic.NewTextContentBlock("I'm doing well, thank you."),
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anthropicFromChatMessages(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
