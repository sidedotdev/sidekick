package temp_common2

import (
	"context"
	"encoding/json"
	"testing"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockKeyValueStorage implements common.KeyValueStorage for testing.
type mockKeyValueStorage struct {
	data map[string][]byte
}

func newMockKeyValueStorage() *mockKeyValueStorage {
	return &mockKeyValueStorage{
		data: make(map[string][]byte),
	}
}

func (m *mockKeyValueStorage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	result := make([][]byte, len(keys))
	for i, key := range keys {
		result[i] = m.data[key]
	}
	return result, nil
}

func (m *mockKeyValueStorage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		m.data[key] = data
	}
	return nil
}

func TestLlm2ChatHistory_NewLlm2ChatHistory(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")

	assert.NotNil(t, h)
	assert.Equal(t, 0, h.Len())
	assert.True(t, h.hydrated)
	assert.Empty(t, h.refs)
	assert.Empty(t, h.messages)
	assert.Empty(t, h.unpersisted)
}

func TestLlm2ChatHistory_Append(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")

	msg := &llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Hello"},
		},
	}

	h.Append(msg)

	assert.Equal(t, 1, h.Len())
	assert.Equal(t, []int{0}, h.unpersisted)
	assert.Equal(t, "user", h.Get(0).GetRole())
	assert.Equal(t, "Hello", h.Get(0).GetContentString())
}

func TestLlm2ChatHistory_Len(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")

	assert.Equal(t, 0, h.Len())

	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "1"}}})
	assert.Equal(t, 1, h.Len())

	h.Append(&llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "2"}}})
	assert.Equal(t, 2, h.Len())
}

func TestLlm2ChatHistory_Get(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "First"}}})
	h.Append(&llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Second"}}})

	t.Run("valid indices", func(t *testing.T) {
		msg := h.Get(0)
		assert.NotNil(t, msg)
		assert.Equal(t, "First", msg.GetContentString())

		msg = h.Get(1)
		assert.NotNil(t, msg)
		assert.Equal(t, "Second", msg.GetContentString())
	})

	t.Run("negative index", func(t *testing.T) {
		msg := h.Get(-1)
		assert.Nil(t, msg)
	})

	t.Run("out of bounds", func(t *testing.T) {
		msg := h.Get(2)
		assert.Nil(t, msg)
	})
}

func TestLlm2ChatHistory_Set(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Original"}}})

	h.Set(0, &llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Updated"}}})

	assert.Equal(t, "Updated", h.Get(0).GetContentString())
	assert.Contains(t, h.unpersisted, 0)
}

func TestLlm2ChatHistory_Messages(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}}})
	h.Append(&llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hi"}}})

	messages := h.Messages()

	assert.Len(t, messages, 2)
	assert.Equal(t, "user", messages[0].GetRole())
	assert.Equal(t, "Hello", messages[0].GetContentString())
	assert.Equal(t, "assistant", messages[1].GetRole())
	assert.Equal(t, "Hi", messages[1].GetContentString())
}

func TestLlm2ChatHistory_MarshalJSON_ProducesRefsOnly(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello world"}}})
	h.Append(&llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hi there"}}})

	// Persist to generate refs
	storage := newMockKeyValueStorage()
	err := h.Persist(context.Background(), storage)
	require.NoError(t, err)

	// Marshal should produce refs-only JSON
	data, err := h.MarshalJSON()
	require.NoError(t, err)

	// Verify it's an array of refs
	var refs []common.MessageRef
	err = json.Unmarshal(data, &refs)
	require.NoError(t, err)

	assert.Len(t, refs, 2)
	assert.Equal(t, "flow-123", refs[0].FlowId)
	assert.Equal(t, "user", refs[0].Role)
	assert.NotEmpty(t, refs[0].BlockIds)
	assert.Equal(t, "flow-123", refs[1].FlowId)
	assert.Equal(t, "assistant", refs[1].Role)
	assert.NotEmpty(t, refs[1].BlockIds)

	// Verify message content is NOT in the JSON
	jsonStr := string(data)
	assert.NotContains(t, jsonStr, "Hello world")
	assert.NotContains(t, jsonStr, "Hi there")
}

func TestLlm2ChatHistory_UnmarshalJSON_SetsNotHydrated(t *testing.T) {
	refs := []common.MessageRef{
		{FlowId: "flow-123", BlockIds: []string{"block-1"}, Role: "user"},
		{FlowId: "flow-123", BlockIds: []string{"block-2"}, Role: "assistant"},
	}
	data, err := json.Marshal(refs)
	require.NoError(t, err)

	h := &Llm2ChatHistory{}
	err = h.UnmarshalJSON(data)
	require.NoError(t, err)

	assert.False(t, h.hydrated)
	assert.Nil(t, h.messages)
	assert.Len(t, h.refs, 2)
	assert.Equal(t, "flow-123", h.refs[0].FlowId)
	assert.Equal(t, "user", h.refs[0].Role)
}

func TestLlm2ChatHistory_Hydrate_RestoresContent(t *testing.T) {
	// Create storage with pre-populated content blocks
	storage := newMockKeyValueStorage()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello from user"}
	block2 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello from assistant"}
	storage.MSet(context.Background(), "workspace-456", map[string]interface{}{
		"block-1": block1,
		"block-2": block2,
	})

	// Create non-hydrated history with refs
	h := &Llm2ChatHistory{
		workspaceId: "workspace-456",
		refs: []common.MessageRef{
			{FlowId: "flow-123", BlockIds: []string{"block-1"}, Role: "user"},
			{FlowId: "flow-123", BlockIds: []string{"block-2"}, Role: "assistant"},
		},
		hydrated: false,
	}

	err := h.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	assert.True(t, h.hydrated)
	assert.Len(t, h.messages, 2)
	assert.Equal(t, "user", h.messages[0].GetRole())
	assert.Equal(t, "Hello from user", h.messages[0].GetContentString())
	assert.Equal(t, "assistant", h.messages[1].GetRole())
	assert.Equal(t, "Hello from assistant", h.messages[1].GetContentString())
}

func TestLlm2ChatHistory_Hydrate_AlreadyHydrated(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}}})

	// Should be a no-op
	err := h.Hydrate(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, 1, h.Len())
	assert.Equal(t, "Hello", h.Get(0).GetContentString())
}

func TestLlm2ChatHistory_Hydrate_EmptyRefs(t *testing.T) {
	h := &Llm2ChatHistory{
		workspaceId: "workspace-456",
		refs:        []common.MessageRef{},
		hydrated:    false,
	}

	err := h.Hydrate(context.Background(), newMockKeyValueStorage())
	require.NoError(t, err)

	assert.True(t, h.hydrated)
	assert.Empty(t, h.messages)
}

func TestLlm2ChatHistory_Persist_StoresContentBlocks(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Hello"},
			{Type: llm2.ContentBlockTypeText, Text: "World"},
		},
	})
	h.Append(&llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Hi there"},
		},
	})

	storage := newMockKeyValueStorage()
	err := h.Persist(context.Background(), storage)
	require.NoError(t, err)

	// Verify refs were updated
	assert.Len(t, h.refs, 2)
	assert.Equal(t, "flow-123", h.refs[0].FlowId)
	assert.Equal(t, "user", h.refs[0].Role)
	assert.Len(t, h.refs[0].BlockIds, 2)
	assert.Equal(t, "flow-123", h.refs[1].FlowId)
	assert.Equal(t, "assistant", h.refs[1].Role)
	assert.Len(t, h.refs[1].BlockIds, 1)

	// Verify content was stored
	assert.Len(t, storage.data, 3)

	// Verify unpersisted was cleared
	assert.Empty(t, h.unpersisted)
}

func TestLlm2ChatHistory_Persist_NothingToPersist(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")

	storage := newMockKeyValueStorage()
	err := h.Persist(context.Background(), storage)
	require.NoError(t, err)

	assert.Empty(t, storage.data)
}

func TestLlm2ChatHistory_Persist_NotHydrated(t *testing.T) {
	h := &Llm2ChatHistory{
		hydrated: false,
	}

	err := h.Persist(context.Background(), newMockKeyValueStorage())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-hydrated")
}

func TestLlm2ChatHistory_RoundTrip(t *testing.T) {
	// Create and populate history
	original := NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Hello, how are you?"},
		},
	})
	original.Append(&llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "I'm doing well, thank you!"},
		},
	})
	original.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Great to hear!"},
		},
	})

	// Persist to storage
	storage := newMockKeyValueStorage()
	err := original.Persist(context.Background(), storage)
	require.NoError(t, err)

	// Marshal to JSON
	data, err := original.MarshalJSON()
	require.NoError(t, err)

	// Unmarshal into new history
	restored := &Llm2ChatHistory{workspaceId: "workspace-456"}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	// Hydrate from storage
	err = restored.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	// Verify content matches
	assert.Equal(t, original.Len(), restored.Len())
	for i := 0; i < original.Len(); i++ {
		assert.Equal(t, original.Get(i).GetRole(), restored.Get(i).GetRole())
		assert.Equal(t, original.Get(i).GetContentString(), restored.Get(i).GetContentString())
	}
}

func TestLlm2ChatHistory_RoundTrip_WithToolCalls(t *testing.T) {
	original := NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{Type: llm2.ContentBlockTypeText, Text: "Search for something"},
		},
	})
	original.Append(&llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        "call_123",
					Name:      "search",
					Arguments: `{"query": "test"}`,
				},
			},
		},
	})
	original.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{
			{
				Type: llm2.ContentBlockTypeToolResult,
				ToolResult: &llm2.ToolResultBlock{
					ToolCallId: "call_123",
					Text:       "Search results here",
				},
			},
		},
	})

	storage := newMockKeyValueStorage()
	err := original.Persist(context.Background(), storage)
	require.NoError(t, err)

	data, err := original.MarshalJSON()
	require.NoError(t, err)

	restored := &Llm2ChatHistory{workspaceId: "workspace-456"}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	err = restored.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	assert.Equal(t, original.Len(), restored.Len())

	// Verify tool call details
	toolCalls := restored.Get(1).GetToolCalls()
	assert.Len(t, toolCalls, 1)
	assert.Equal(t, "call_123", toolCalls[0].Id)
	assert.Equal(t, "search", toolCalls[0].Name)
}

func TestLlm2ChatHistory_AccessorsPanicWhenNotHydrated(t *testing.T) {
	h := &Llm2ChatHistory{
		hydrated: false,
		refs: []common.MessageRef{
			{FlowId: "flow-123", BlockIds: []string{"block-1"}, Role: "user"},
		},
	}

	t.Run("Len panics", func(t *testing.T) {
		assert.Panics(t, func() {
			h.Len()
		})
	})

	t.Run("Get panics", func(t *testing.T) {
		assert.Panics(t, func() {
			h.Get(0)
		})
	})

	t.Run("Set panics", func(t *testing.T) {
		assert.Panics(t, func() {
			h.Set(0, &llm2.Message{})
		})
	})

	t.Run("Messages panics", func(t *testing.T) {
		assert.Panics(t, func() {
			h.Messages()
		})
	})

	t.Run("Append panics", func(t *testing.T) {
		assert.Panics(t, func() {
			h.Append(&llm2.Message{})
		})
	})
}

func TestChatHistoryContainer_UnmarshalJSON_DetectsMessageRefFormat(t *testing.T) {
	// Ensure the factory is registered
	_ = NewLlm2ChatHistory("", "")

	refs := []common.MessageRef{
		{FlowId: "flow-123", BlockIds: []string{"block-1", "block-2"}, Role: "user"},
		{FlowId: "flow-123", BlockIds: []string{"block-3"}, Role: "assistant"},
	}
	data, err := json.Marshal(refs)
	require.NoError(t, err)

	var container common.ChatHistoryContainer
	err = json.Unmarshal(data, &container)
	require.NoError(t, err)

	assert.NotNil(t, container.History)
	_, ok := container.History.(*Llm2ChatHistory)
	assert.True(t, ok, "History should be *Llm2ChatHistory")
}

func TestChatHistoryContainer_UnmarshalJSON_FallsBackToLegacy(t *testing.T) {
	msgs := []common.ChatMessage{
		{Role: common.ChatMessageRoleUser, Content: "Hello"},
		{Role: common.ChatMessageRoleAssistant, Content: "Hi there"},
	}
	data, err := json.Marshal(msgs)
	require.NoError(t, err)

	var container common.ChatHistoryContainer
	err = json.Unmarshal(data, &container)
	require.NoError(t, err)

	assert.NotNil(t, container.History)
	_, ok := container.History.(*common.LegacyChatHistory)
	assert.True(t, ok, "History should be *LegacyChatHistory")

	assert.Equal(t, 2, container.History.Len())
	assert.Equal(t, "Hello", container.History.Get(0).GetContentString())
}

func TestLlm2ChatHistory_Llm2Messages(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.Append(&llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}}})
	h.Append(&llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hi"}}})

	messages := h.Llm2Messages()

	assert.Len(t, messages, 2)
	assert.Equal(t, llm2.RoleUser, messages[0].Role)
	assert.Equal(t, "Hello", messages[0].Content[0].Text)
	assert.Equal(t, llm2.RoleAssistant, messages[1].Role)
	assert.Equal(t, "Hi", messages[1].Content[0].Text)
}

func TestLlm2ChatHistory_SetFlowId(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.SetFlowId("new-flow-id")
	assert.Equal(t, "new-flow-id", h.flowId)
}

func TestLlm2ChatHistory_SetWorkspaceId(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")
	h.SetWorkspaceId("new-workspace-id")
	assert.Equal(t, "new-workspace-id", h.workspaceId)
}

func TestLlm2ChatHistory_SetMessages(t *testing.T) {
	h := NewLlm2ChatHistory("flow-123", "workspace-456")

	// Add initial message
	h.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "initial"}},
	})

	// Replace with new messages
	newMessages := []llm2.Message{
		{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "first"}}},
		{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "second"}}},
		{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "third"}}},
	}
	h.SetMessages(newMessages)

	assert.Equal(t, 3, h.Len())
	assert.Equal(t, "first", h.Get(0).GetContentString())
	assert.Equal(t, "second", h.Get(1).GetContentString())
	assert.Equal(t, "third", h.Get(2).GetContentString())

	// Verify all messages are marked as unpersisted
	assert.Equal(t, []int{0, 1, 2}, h.unpersisted)

	// Verify refs are reset
	assert.Equal(t, 3, len(h.refs))
}

func TestLlm2ChatHistory_SetMessages_PanicsWhenNotHydrated(t *testing.T) {
	h := &Llm2ChatHistory{
		flowId:      "flow-123",
		workspaceId: "workspace-456",
		hydrated:    false,
	}

	assert.Panics(t, func() {
		h.SetMessages([]llm2.Message{})
	})
}

func TestLlm2ChatHistory_Hydrate_MultipleBlocksPerMessage(t *testing.T) {
	storage := newMockKeyValueStorage()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Part 1"}
	block2 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Part 2"}
	block3 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Part 3"}
	storage.MSet(context.Background(), "workspace-456", map[string]interface{}{
		"block-1": block1,
		"block-2": block2,
		"block-3": block3,
	})

	h := &Llm2ChatHistory{
		workspaceId: "workspace-456",
		refs: []common.MessageRef{
			{FlowId: "flow-123", BlockIds: []string{"block-1", "block-2"}, Role: "user"},
			{FlowId: "flow-123", BlockIds: []string{"block-3"}, Role: "assistant"},
		},
		hydrated: false,
	}

	err := h.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	assert.Len(t, h.messages, 2)
	assert.Len(t, h.messages[0].Content, 2)
	assert.Equal(t, "Part 1", h.messages[0].Content[0].Text)
	assert.Equal(t, "Part 2", h.messages[0].Content[1].Text)
	assert.Len(t, h.messages[1].Content, 1)
	assert.Equal(t, "Part 3", h.messages[1].Content[0].Text)
}

func TestLlm2ChatHistory_Hydrate_RestoresFlowId(t *testing.T) {
	storage := newMockKeyValueStorage()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello"}
	storage.MSet(context.Background(), "workspace-456", map[string]interface{}{
		"block-1": block1,
	})

	h := &Llm2ChatHistory{
		workspaceId: "workspace-456",
		refs: []common.MessageRef{
			{FlowId: "flow-123", BlockIds: []string{"block-1"}, Role: "user"},
		},
		hydrated: false,
	}

	// flowId is empty before hydration
	assert.Equal(t, "", h.flowId)

	err := h.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	// flowId should be restored from refs
	assert.Equal(t, "flow-123", h.flowId)
}

func TestLlm2ChatHistory_Hydrate_PreservesExistingFlowId(t *testing.T) {
	storage := newMockKeyValueStorage()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello"}
	storage.MSet(context.Background(), "workspace-456", map[string]interface{}{
		"block-1": block1,
	})

	h := &Llm2ChatHistory{
		flowId:      "existing-flow-id",
		workspaceId: "workspace-456",
		refs: []common.MessageRef{
			{FlowId: "flow-123", BlockIds: []string{"block-1"}, Role: "user"},
		},
		hydrated: false,
	}

	err := h.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	// flowId should be preserved, not overwritten
	assert.Equal(t, "existing-flow-id", h.flowId)
}
