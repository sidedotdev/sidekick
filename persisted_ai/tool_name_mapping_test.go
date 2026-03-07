package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm2"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testToolNameMappingConfig(forward map[string]string) *ToolNameMappingConfig {
	reverse := make(map[string]string, len(forward))
	for sourceName, mappedName := range forward {
		reverse[mappedName] = sourceName
	}

	return &ToolNameMappingConfig{
		Forward: forward,
		Reverse: reverse,
	}
}
func TestMapOptionsToolNames(t *testing.T) {
	t.Parallel()

	config := testToolNameMappingConfig(map[string]string{
		"read_file_lines":   "Read",
		"get_help_or_input": "AskUserQuestion",
		"done":              "mcp__tu__done",
	})

	originalTool := &common.Tool{
		Name:        "read_file_lines",
		Description: "read file",
	}
	options := llm2.Options{
		Tools: []*common.Tool{
			originalTool,
			nil,
			{
				Name:        "get_help_or_input",
				Description: "ask the user",
			},
			{
				Name:        "unmapped",
				Description: "unchanged",
			},
		},
		ToolChoice: common.ToolChoice{
			Type: common.ToolChoiceTypeTool,
			Name: "done",
		},
	}

	mapped := mapOptionsToolNames(options, config)

	assert.Equal(t, "Read", mapped.Tools[0].Name)
	assert.Nil(t, mapped.Tools[1])
	assert.Equal(t, "AskUserQuestion", mapped.Tools[2].Name)
	assert.Equal(t, "unmapped", mapped.Tools[3].Name)
	assert.Equal(t, "mcp__tu__done", mapped.ToolChoice.Name)

	assert.Equal(t, "read_file_lines", originalTool.Name)
	assert.Equal(t, "get_help_or_input", options.Tools[2].Name)
	assert.Equal(t, "done", options.ToolChoice.Name)
}
func TestMapMessagesToolNames(t *testing.T) {
	t.Parallel()

	config := testToolNameMappingConfig(map[string]string{
		"read_file_lines":   "Read",
		"done":              "mcp__tu__done",
		"get_help_or_input": "AskUserQuestion",
	})

	messages := []llm2.Message{
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "call-1",
						Name:      "read_file_lines",
						Arguments: `{"path":"foo.go"}`,
					},
				},
			},
		},
		{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeToolResult,
					ToolResult: &llm2.ToolResultBlock{
						ToolCallId: "call-1",
						Name:       "done",
						Content: []llm2.ContentBlock{
							{
								Type: llm2.ContentBlockTypeText,
								Text: "ok",
							},
							{
								Type: llm2.ContentBlockTypeToolUse,
								ToolUse: &llm2.ToolUseBlock{
									Id:        "call-2",
									Name:      "get_help_or_input",
									Arguments: `{"question":"continue?"}`,
								},
							},
						},
					},
				},
			},
		},
	}

	mapped := mapMessagesToolNames(messages, config)

	assert.Equal(t, "Read", mapped[0].Content[0].ToolUse.Name)
	assert.Equal(t, "mcp__tu__done", mapped[1].Content[0].ToolResult.Name)
	assert.Equal(t, "AskUserQuestion", mapped[1].Content[0].ToolResult.Content[1].ToolUse.Name)

	assert.Equal(t, "read_file_lines", messages[0].Content[0].ToolUse.Name)
	assert.Equal(t, "done", messages[1].Content[0].ToolResult.Name)
	assert.Equal(t, "get_help_or_input", messages[1].Content[0].ToolResult.Content[1].ToolUse.Name)
}
func TestReverseMapMessageToolNames(t *testing.T) {
	t.Parallel()

	config := testToolNameMappingConfig(map[string]string{
		"read_file_lines":   "Read",
		"get_help_or_input": "AskUserQuestion",
		"done":              "mcp__tu__done",
		"set_base_branch":   "mcp__tu__set_base_branch",
	})

	message := llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call-1",
					Name:      "Read",
					Arguments: `{}`,
				},
			},
			{
				Type: llm2.ContentBlockTypeToolResult,
				ToolResult: &llm2.ToolResultBlock{
					ToolCallId: "call-2",
					Name:       "mcp__tu__done",
					Content: []llm2.ContentBlock{
						{
							Type: llm2.ContentBlockTypeToolUse,
							ToolUse: &llm2.ToolUseBlock{
								Id:        "call-3",
								Name:      "AskUserQuestion",
								Arguments: `{}`,
							},
						},
						{
							Type: llm2.ContentBlockTypeToolResult,
							ToolResult: &llm2.ToolResultBlock{
								ToolCallId: "call-4",
								Name:       "mcp__tu__set_base_branch",
								Content: []llm2.ContentBlock{
									{
										Type: llm2.ContentBlockTypeText,
										Text: "ok",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	reversed := reverseMapMessageToolNames(message, config)

	assert.Equal(t, "read_file_lines", reversed.Content[0].ToolUse.Name)
	assert.Equal(t, "done", reversed.Content[1].ToolResult.Name)
	assert.Equal(t, "get_help_or_input", reversed.Content[1].ToolResult.Content[0].ToolUse.Name)
	assert.Equal(t, "set_base_branch", reversed.Content[1].ToolResult.Content[1].ToolResult.Name)

	assert.Equal(t, "Read", message.Content[0].ToolUse.Name)
	assert.Equal(t, "mcp__tu__done", message.Content[1].ToolResult.Name)
	assert.Equal(t, "AskUserQuestion", message.Content[1].ToolResult.Content[0].ToolUse.Name)
	assert.Equal(t, "mcp__tu__set_base_branch", message.Content[1].ToolResult.Content[1].ToolResult.Name)
}
func TestMapMessagesToolNames_WithPrefix(t *testing.T) {
	t.Parallel()

	config := &ToolNameMappingConfig{
		Forward: map[string]string{
			"read_file_lines": "Read",
		},
		Reverse: map[string]string{
			"Read": "read_file_lines",
		},
		Prefix: "mcp__tu__",
	}

	messages := []llm2.Message{
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "call-1",
						Name:      "read_file_lines",
						Arguments: `{}`,
					},
				},
				{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "call-2",
						Name:      "done",
						Arguments: `{}`,
					},
				},
			},
		},
	}

	mapped := mapMessagesToolNames(messages, config)

	assert.Equal(t, "Read", mapped[0].Content[0].ToolUse.Name)
	assert.Equal(t, "mcp__tu__done", mapped[0].Content[1].ToolUse.Name)
	assert.Equal(t, "read_file_lines", messages[0].Content[0].ToolUse.Name)
	assert.Equal(t, "done", messages[0].Content[1].ToolUse.Name)
}

func TestMapOptionsToolNames_WithPrefix(t *testing.T) {
	t.Parallel()

	config := &ToolNameMappingConfig{
		Forward: map[string]string{
			"read_file_lines": "Read",
		},
		Reverse: map[string]string{
			"Read": "read_file_lines",
		},
		Prefix: "mcp__tu__",
	}

	originalTool := &common.Tool{
		Name:        "read_file_lines",
		Description: "read file",
	}
	options := llm2.Options{
		Tools: []*common.Tool{
			originalTool,
			{
				Name:        "done",
				Description: "finish",
			},
		},
		ToolChoice: common.ToolChoice{
			Type: common.ToolChoiceTypeTool,
			Name: "done",
		},
	}

	mapped := mapOptionsToolNames(options, config)

	assert.Equal(t, "Read", mapped.Tools[0].Name)
	assert.Equal(t, "mcp__tu__done", mapped.Tools[1].Name)
	assert.Equal(t, "mcp__tu__done", mapped.ToolChoice.Name)

	assert.Equal(t, "read_file_lines", originalTool.Name)
	assert.Equal(t, "done", options.Tools[1].Name)
	assert.Equal(t, "done", options.ToolChoice.Name)
}
