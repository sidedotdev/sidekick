package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm2"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testToolNameMapper struct {
	forward map[string]string
	reverse map[string]string
}

func (m testToolNameMapper) MapToolName(name string) string {
	if mappedName, ok := m.forward[name]; ok {
		return mappedName
	}
	return name
}

func (m testToolNameMapper) ReverseMapToolName(name string) string {
	if mappedName, ok := m.reverse[name]; ok {
		return mappedName
	}
	return name
}

func testToolNameMapperPtr(forward map[string]string) *ToolNameMapper {
	reverse := make(map[string]string, len(forward))
	for sourceName, mappedName := range forward {
		reverse[mappedName] = sourceName
	}

	var mapper ToolNameMapper = testToolNameMapper{
		forward: forward,
		reverse: reverse,
	}
	return &mapper
}
func TestMapOptionsToolNames(t *testing.T) {
	t.Parallel()

	mapper := testToolNameMapperPtr(map[string]string{
		"read_file_lines": "Read",
		"done":            "Done",
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
				Name:        "unmapped",
				Description: "unchanged",
			},
		},
		ToolChoice: common.ToolChoice{
			Type: common.ToolChoiceTypeTool,
			Name: "done",
		},
	}

	mapped := mapOptionsToolNames(options, mapper)

	assert.Equal(t, "Read", mapped.Tools[0].Name)
	assert.Nil(t, mapped.Tools[1])
	assert.Equal(t, "unmapped", mapped.Tools[2].Name)
	assert.Equal(t, "Done", mapped.ToolChoice.Name)

	assert.Equal(t, "read_file_lines", originalTool.Name)
	assert.Equal(t, "done", options.ToolChoice.Name)
}
func TestMapMessagesToolNames(t *testing.T) {
	t.Parallel()

	mapper := testToolNameMapperPtr(map[string]string{
		"read_file_lines":        "Read",
		"bulk_search_repository": "Grep",
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
						Name:       "read_file_lines",
						Content: []llm2.ContentBlock{
							{
								Type: llm2.ContentBlockTypeText,
								Text: "ok",
							},
							{
								Type: llm2.ContentBlockTypeToolUse,
								ToolUse: &llm2.ToolUseBlock{
									Id:        "call-2",
									Name:      "bulk_search_repository",
									Arguments: `{"query":"foo"}`,
								},
							},
						},
					},
				},
			},
		},
	}

	mapped := mapMessagesToolNames(messages, mapper)

	assert.Equal(t, "Read", mapped[0].Content[0].ToolUse.Name)
	assert.Equal(t, "Read", mapped[1].Content[0].ToolResult.Name)
	assert.Equal(t, "Grep", mapped[1].Content[0].ToolResult.Content[1].ToolUse.Name)

	assert.Equal(t, "read_file_lines", messages[0].Content[0].ToolUse.Name)
	assert.Equal(t, "read_file_lines", messages[1].Content[0].ToolResult.Name)
	assert.Equal(t, "bulk_search_repository", messages[1].Content[0].ToolResult.Content[1].ToolUse.Name)
}
func TestReverseMapMessageToolNames(t *testing.T) {
	t.Parallel()

	mapper := testToolNameMapperPtr(map[string]string{
		"read_file_lines":        "Read",
		"bulk_search_repository": "Grep",
		"done":                   "Done",
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
					Name:       "Grep",
					Content: []llm2.ContentBlock{
						{
							Type: llm2.ContentBlockTypeToolUse,
							ToolUse: &llm2.ToolUseBlock{
								Id:        "call-3",
								Name:      "Done",
								Arguments: `{}`,
							},
						},
					},
				},
			},
		},
	}

	reversed := reverseMapMessageToolNames(message, mapper)

	assert.Equal(t, "read_file_lines", reversed.Content[0].ToolUse.Name)
	assert.Equal(t, "bulk_search_repository", reversed.Content[1].ToolResult.Name)
	assert.Equal(t, "done", reversed.Content[1].ToolResult.Content[0].ToolUse.Name)

	assert.Equal(t, "Read", message.Content[0].ToolUse.Name)
	assert.Equal(t, "Grep", message.Content[1].ToolResult.Name)
	assert.Equal(t, "Done", message.Content[1].ToolResult.Content[0].ToolUse.Name)
}
