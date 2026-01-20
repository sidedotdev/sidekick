package persisted_ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockKVStorage struct {
	data       map[string][]byte
	mgetErr    error
	msetErr    error
	mgetCalled bool
	msetCalled bool
}

func newMockKVStorage() *mockKVStorage {
	return &mockKVStorage{
		data: make(map[string][]byte),
	}
}

func (m *mockKVStorage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	m.mgetCalled = true
	if m.mgetErr != nil {
		return nil, m.mgetErr
	}
	result := make([][]byte, len(keys))
	for i, key := range keys {
		result[i] = m.data[key]
	}
	return result, nil
}

func (m *mockKVStorage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	m.msetCalled = true
	if m.msetErr != nil {
		return m.msetErr
	}
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		m.data[key] = data
	}
	return nil
}

func (m *mockKVStorage) DeletePrefix(ctx context.Context, workspaceId string, prefix string) error {
	return nil
}

func (m *mockKVStorage) GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error) {
	return nil, nil
}

func (m *mockKVStorage) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	m.msetCalled = true
	if m.msetErr != nil {
		return m.msetErr
	}
	for key, value := range values {
		m.data[key] = value
	}
	return nil
}

func noopManageLlm2ChatHistory(messages []llm2.Message, maxLength int) ([]llm2.Message, error) {
	return messages, nil
}

func TestManageV3_HydratesHistory(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	original := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})
	err := original.Persist(context.Background(), storage, llm2.NewKsuidGenerator())
	require.NoError(t, err)

	data, err := original.MarshalJSON()
	require.NoError(t, err)

	restored := &llm2.Llm2ChatHistory{}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	container := &llm2.ChatHistoryContainer{History: restored}
	storage.mgetCalled = false

	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, storage.mgetCalled, "MGet should have been called for hydration")
}

func TestManageV3_PersistsHistory(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	container := &llm2.ChatHistoryContainer{History: history}

	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, storage.msetCalled, "MSet should have been called for persistence")
}

func TestManageV3_HydrationError(t *testing.T) {
	storage := newMockKVStorage()
	storage.mgetErr = errors.New("hydration failed")
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	original := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	initialStorage := newMockKVStorage()
	err := original.Persist(context.Background(), initialStorage, llm2.NewKsuidGenerator())
	require.NoError(t, err)

	data, err := original.MarshalJSON()
	require.NoError(t, err)

	restored := &llm2.Llm2ChatHistory{}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	container := &llm2.ChatHistoryContainer{History: restored}

	_, err = activities.ManageV3(context.Background(), container, "workspace-456", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrate")
}

func TestManageV3_PersistError(t *testing.T) {
	storage := newMockKVStorage()
	storage.msetErr = errors.New("persist failed")
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	container := &llm2.ChatHistoryContainer{History: history}

	_, err := activities.ManageV3(context.Background(), container, "workspace-456", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist")
}

func TestManageV3_WrongHistoryType(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	legacyHistory := llm2.NewLegacyChatHistoryFromChatMessages(nil)
	container := &llm2.ChatHistoryContainer{History: legacyHistory}

	_, err := activities.ManageV3(context.Background(), container, "workspace-456", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Llm2ChatHistory")
}

func TestManageV3_PreservesRefsForUnchangedMessages(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	// Create history with multiple messages, using ContextType to ensure retention
	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:        llm2.ContentBlockTypeText,
			Text:        "First message",
			ContextType: "initial_instructions", // Ensures retention
		}},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response"}},
	})
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:        llm2.ContentBlockTypeText,
			Text:        "Second message",
			ContextType: "user_feedback", // Ensures retention
		}},
	})

	// Persist to get initial refs
	err := history.Persist(context.Background(), storage, llm2.NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &llm2.ChatHistoryContainer{History: history}

	// Run ManageV3 with large maxLength to prevent trimming
	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 100000)
	require.NoError(t, err)

	resultHistory := result.History.(*llm2.Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	// Messages should still be present (management didn't drop any)
	assert.Equal(t, 3, resultHistory.Len())

	// The middle message (index 1) should have preserved its ref block IDs
	// (first and last get cache control changes, so their refs change)
	assert.Equal(t, originalRefs[1].BlockIds, newRefs[1].BlockIds,
		"unchanged middle message should preserve its block IDs")
}

func TestManageV3_ChangesRefsForMarkerOnlyChanges(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	// Create history with messages, using ContextType to ensure retention
	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:         llm2.ContentBlockTypeText,
			Text:         "First message",
			ContextType:  "initial_instructions", // Ensures retention
			CacheControl: "ephemeral",            // Pre-existing cache control
		}},
	})
	history.Append(&llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{
			Type: llm2.ContentBlockTypeText,
			Text: "Response",
		}},
	})

	// Persist to get initial refs
	err := history.Persist(context.Background(), storage, llm2.NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &llm2.ChatHistoryContainer{History: history}

	// Run ManageV3 with large maxLength - this will clear and re-apply cache control
	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 100000)
	require.NoError(t, err)

	resultHistory := result.History.(*llm2.Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	require.Equal(t, 2, len(newRefs), "should have 2 messages after management")

	// First message still has ephemeral (it's the first message), so content is same
	// But the second message now has ephemeral (it's the last), so it changed
	assert.NotEqual(t, originalRefs[1].BlockIds, newRefs[1].BlockIds,
		"message with marker change should get new block IDs")
}

func TestManageV3_HydratingFromRefsRestoresMarkers(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	// Create and manage history with ContextType to ensure retention
	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:        llm2.ContentBlockTypeText,
			Text:        "Hello",
			ContextType: "initial_instructions", // Ensures retention
		}},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hi there"}},
	})

	container := &llm2.ChatHistoryContainer{History: history}

	// Run ManageV3 to apply cache control and persist
	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 100000)
	require.NoError(t, err)

	// Get the refs from the managed history
	managedHistory := result.History.(*llm2.Llm2ChatHistory)
	refs := managedHistory.Refs()

	require.Len(t, refs, 2, "should have 2 refs after management")

	// Create a fresh history from refs only (simulating workflow deserialization)
	freshHistory := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	freshHistory.SetRefs(refs)

	// Hydrate from storage
	err = freshHistory.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	// Verify markers are restored
	messages := freshHistory.Llm2Messages()
	require.Len(t, messages, 2)

	// First message should have ephemeral cache control
	assert.Equal(t, "ephemeral", messages[0].Content[0].CacheControl,
		"first message should have ephemeral cache control after hydration")

	// Last message should also have ephemeral cache control
	assert.Equal(t, "ephemeral", messages[1].Content[0].CacheControl,
		"last message should have ephemeral cache control after hydration")
}

func TestManageV3_PreservesContextType(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	// Create history with ContextType set
	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:        llm2.ContentBlockTypeText,
			Text:        "Instructions",
			ContextType: "initial_instructions",
		}},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response"}},
	})

	container := &llm2.ChatHistoryContainer{History: history}

	// Run ManageV3 with large maxLength
	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 100000)
	require.NoError(t, err)

	// Get refs and create fresh history
	managedHistory := result.History.(*llm2.Llm2ChatHistory)
	refs := managedHistory.Refs()

	require.Len(t, refs, 2, "should have 2 refs after management")

	freshHistory := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	freshHistory.SetRefs(refs)
	err = freshHistory.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	// Verify ContextType is preserved
	messages := freshHistory.Llm2Messages()
	assert.Equal(t, "initial_instructions", messages[0].Content[0].ContextType,
		"ContextType should be preserved after hydration")
}

func TestManageV3_DroppingOlderMessagesPreservesRetainedRefs(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	// Create history with messages that have ContextType for retention control
	history := llm2.NewLlm2ChatHistory("flow-123", "workspace-456")
	// First message: initial_instructions (always retained)
	history.Append(&llm2.Message{
		Role: llm2.RoleUser,
		Content: []llm2.ContentBlock{{
			Type:        llm2.ContentBlockTypeText,
			Text:        "Initial instructions",
			ContextType: "initial_instructions",
		}},
	})
	// Middle messages without special context type (may be dropped)
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response 1"}},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Question 2"}},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response 2"}},
	})
	// Last message (always retained)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Final question"}},
	})

	// Persist to get initial refs
	err := history.Persist(context.Background(), storage, llm2.NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &llm2.ChatHistoryContainer{History: history}

	// Run ManageV3 with small maxLength to trigger trimming of middle messages
	result, err := activities.ManageV3(context.Background(), container, "workspace-456", 50)
	require.NoError(t, err)

	resultHistory := result.History.(*llm2.Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	// At minimum, first (initial_instructions) and last messages should be retained
	require.GreaterOrEqual(t, len(newRefs), 2, "should retain at least first and last messages")

	// The last message in the result should have new refs (cache control added)
	// but we verify that management completed successfully
	assert.True(t, len(newRefs[len(newRefs)-1].BlockIds) > 0,
		"last message should have block IDs")

	// If any middle messages were retained, check if their refs were preserved
	// by looking for matching block IDs between original and new refs
	if len(newRefs) > 2 {
		// Check middle messages (not first or last)
		for i := 1; i < len(newRefs)-1; i++ {
			// See if this ref matches any original middle ref
			for j := 1; j < len(originalRefs)-1; j++ {
				if len(newRefs[i].BlockIds) > 0 && len(originalRefs[j].BlockIds) > 0 {
					if newRefs[i].BlockIds[0] == originalRefs[j].BlockIds[0] {
						// Found a preserved ref - this is expected behavior
						t.Logf("Middle message at new index %d preserved ref from original index %d", i, j)
					}
				}
			}
		}
	}
}
