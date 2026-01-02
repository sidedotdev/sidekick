package persisted_ai

import (
	"strings"
	"testing"

	"sidekick/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureToolCallIds(t *testing.T) {
	tests := []struct {
		name           string
		toolCalls      []llm.ToolCall
		expectedPrefix string
		checkExisting  bool
		existingId     string
	}{
		{
			name: "missing ID gets generated",
			toolCalls: []llm.ToolCall{
				{Id: "", Name: "test_tool", Arguments: "{}"},
			},
			expectedPrefix: "sidetc_",
		},
		{
			name: "existing ID is not modified",
			toolCalls: []llm.ToolCall{
				{Id: "existing_id_123", Name: "test_tool", Arguments: "{}"},
			},
			checkExisting: true,
			existingId:    "existing_id_123",
		},
		{
			name: "multiple missing IDs get unique generated IDs",
			toolCalls: []llm.ToolCall{
				{Id: "", Name: "tool1", Arguments: "{}"},
				{Id: "", Name: "tool2", Arguments: "{}"},
				{Id: "", Name: "tool3", Arguments: "{}"},
			},
			expectedPrefix: "sidetc_",
		},
		{
			name: "mixed existing and missing IDs",
			toolCalls: []llm.ToolCall{
				{Id: "keep_this", Name: "tool1", Arguments: "{}"},
				{Id: "", Name: "tool2", Arguments: "{}"},
				{Id: "also_keep", Name: "tool3", Arguments: "{}"},
			},
			expectedPrefix: "sidetc_",
		},
		{
			name:      "empty tool calls slice",
			toolCalls: []llm.ToolCall{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &llm.ChatMessageResponse{
				ChatMessage: llm.ChatMessage{
					ToolCalls: tt.toolCalls,
				},
			}

			ensureToolCallIds(response)

			if tt.checkExisting {
				require.Len(t, response.ToolCalls, 1)
				assert.Equal(t, tt.existingId, response.ToolCalls[0].Id)
				return
			}

			for _, tc := range response.ToolCalls {
				assert.NotEmpty(t, tc.Id, "tool call ID should not be empty")
			}

			if tt.expectedPrefix != "" {
				for _, tc := range response.ToolCalls {
					if !strings.HasPrefix(tc.Id, "keep_this") && !strings.HasPrefix(tc.Id, "also_keep") {
						assert.True(t, strings.HasPrefix(tc.Id, tt.expectedPrefix),
							"generated ID %q should have prefix %q", tc.Id, tt.expectedPrefix)
					}
				}
			}
		})
	}
}

func TestEnsureToolCallIds_UniqueIds(t *testing.T) {
	response := &llm.ChatMessageResponse{
		ChatMessage: llm.ChatMessage{
			ToolCalls: []llm.ToolCall{
				{Id: "", Name: "tool1", Arguments: "{}"},
				{Id: "", Name: "tool2", Arguments: "{}"},
				{Id: "", Name: "tool3", Arguments: "{}"},
			},
		},
	}

	ensureToolCallIds(response)

	ids := make(map[string]bool)
	for _, tc := range response.ToolCalls {
		assert.False(t, ids[tc.Id], "duplicate ID found: %s", tc.Id)
		ids[tc.Id] = true
	}
}

func TestEnsureToolCallIds_PreservesExistingIds(t *testing.T) {
	response := &llm.ChatMessageResponse{
		ChatMessage: llm.ChatMessage{
			ToolCalls: []llm.ToolCall{
				{Id: "existing_1", Name: "tool1", Arguments: "{}"},
				{Id: "", Name: "tool2", Arguments: "{}"},
				{Id: "existing_2", Name: "tool3", Arguments: "{}"},
			},
		},
	}

	ensureToolCallIds(response)

	assert.Equal(t, "existing_1", response.ToolCalls[0].Id)
	assert.True(t, strings.HasPrefix(response.ToolCalls[1].Id, "sidetc_"))
	assert.Equal(t, "existing_2", response.ToolCalls[2].Id)
}
