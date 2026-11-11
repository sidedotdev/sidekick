package persisted_ai

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapMessagesToolNames_HistoricalArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		tool      string
		arguments string
		want      string
	}{
		{
			name:      "legacy field",
			tool:      "get_symbol_definitions",
			arguments: `{"analysis":"Read definitions","requests":[{"file_path":"main.go","symbols":[{"name":"main"}]}]}`,
			want:      `{"description":"Read definitions","requests":[{"file_path":"main.go","symbols":[{"name":"main"}]}]}`,
		},
		{
			name:      "current field wins",
			tool:      "get_symbol_definitions",
			arguments: `{"analysis":"Old description","description":"Current description","requests":[]}`,
			want:      `{"description":"Current description","requests":[]}`,
		},
		{
			name:      "current arguments unchanged",
			tool:      "get_symbol_definitions",
			arguments: `{"description": "Read definitions", "requests": []}`,
		},
		{
			name:      "unrelated tool unchanged",
			tool:      "get_help_or_input",
			arguments: `{"analysis":"Other tool argument"}`,
		},
		{
			name:      "nested fields and text preserved",
			tool:      "get_symbol_definitions",
			arguments: `{"analysis":"Find analysis","requests":[{"analysis":"nested","file_path":"analysis.go","symbols":[{"name":"analysis"}]}]}`,
			want:      `{"description":"Find analysis","requests":[{"analysis":"nested","file_path":"analysis.go","symbols":[{"name":"analysis"}]}]}`,
		},
		{
			name:      "malformed arguments unchanged",
			tool:      "get_symbol_definitions",
			arguments: `{"analysis":`,
		},
		{
			name:      "non-object arguments unchanged",
			tool:      "get_symbol_definitions",
			arguments: `["analysis"]`,
		},
		{
			name:      "null arguments unchanged",
			tool:      "get_symbol_definitions",
			arguments: `null`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, config := range []*ToolNameMappingConfig{nil, {Prefix: "mcp__tu__"}} {
				block := llm2.ContentBlock{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "call-1",
						Name:      tc.tool,
						Arguments: tc.arguments,
					},
				}
				messages := []llm2.Message{
					{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{block}},
					{
						Role: llm2.RoleUser,
						Content: []llm2.ContentBlock{{
							Type: llm2.ContentBlockTypeToolResult,
							ToolResult: &llm2.ToolResultBlock{
								ToolCallId: "outer-call",
								Name:       "other_tool",
								Content:    []llm2.ContentBlock{block},
							},
						}},
					},
				}

				mapped := mapMessagesToolNames(messages, config)
				for _, toolUse := range []*llm2.ToolUseBlock{
					mapped[0].Content[0].ToolUse,
					mapped[1].Content[0].ToolResult.Content[0].ToolUse,
				} {
					if tc.want == "" {
						assert.Equal(t, tc.arguments, toolUse.Arguments)
					} else {
						assert.JSONEq(t, tc.want, toolUse.Arguments)
					}
					assert.Equal(t, mapToolName(tc.tool, config, false), toolUse.Name)
					assert.Equal(t, "call-1", toolUse.Id)
				}
				assert.Equal(t, tc.arguments, block.ToolUse.Arguments)
				assert.Equal(t, tc.tool, block.ToolUse.Name)
			}
		})
	}
}

func TestStreamActivity_HistoricalToolArguments(t *testing.T) {
	t.Parallel()

	for _, aliases := range []bool{false, true} {
		name := "without aliases"
		if aliases {
			name = "with aliases"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &scriptedProvider{attempts: [][]llm2.Event{{
				{Type: llm2.EventBlockStarted, Index: 0, ContentBlock: &llm2.ContentBlock{Type: llm2.ContentBlockTypeText}},
				{Type: llm2.EventBlockDone, Index: 0, ContentBlock: &llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "ok"}},
			}}}
			activities := &Llm2Activities{
				Streamer: newRecordingFlowEventStreamer(),
				providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
					return provider, nil
				},
			}
			input := newRunawayStreamActivityInput(t)
			history := input.ChatHistory.History.(*Llm2ChatHistory)
			arguments := `{"analysis":"Read definitions","requests":[]}`
			history.SetMessages([]llm2.Message{{
				Role: llm2.RoleAssistant,
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "call-1",
						Name:      "get_symbol_definitions",
						Arguments: arguments,
					},
				}},
			}, {
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolResult,
					ToolResult: &llm2.ToolResultBlock{
						ToolCallId: "call-1",
						Name:       "get_symbol_definitions",
						Content:    llm2.TextContentBlocks("definition"),
					},
				}},
			}})
			if aliases {
				input.ToolNameMapping = &ToolNameMappingConfig{Prefix: "mcp__tu__"}
			}

			_, err := activities.Stream(context.Background(), input)
			require.NoError(t, err)
			require.Len(t, provider.requests, 1)
			request := provider.requests[0]
			assert.JSONEq(t, `{"description":"Read definitions","requests":[]}`, request.Messages[0].Content[0].ToolUse.Arguments)
			assert.Equal(t, mapToolName("get_symbol_definitions", input.ToolNameMapping, false), request.Messages[0].Content[0].ToolUse.Name)
			assert.Equal(t, arguments, history.Llm2Messages()[0].Content[0].ToolUse.Arguments)
		})
	}
}
