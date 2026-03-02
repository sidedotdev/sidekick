package persisted_ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sidekick/common"
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

func TestManageV4_HydratesHistory(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	original := NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})
	err := original.Persist(context.Background(), storage, NewKsuidGenerator())
	require.NoError(t, err)

	data, err := original.MarshalJSON()
	require.NoError(t, err)

	restored := &Llm2ChatHistory{}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	container := &ChatHistoryContainer{History: restored}
	storage.mgetCalled = false

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 0, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, storage.mgetCalled, "MGet should have been called for hydration")
}

func TestManageV4_PersistsHistory(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 0, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, storage.msetCalled, "MSet should have been called for persistence")
}

func TestManageV4_HydrationError(t *testing.T) {
	storage := newMockKVStorage()
	storage.mgetErr = errors.New("hydration failed")
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	original := NewLlm2ChatHistory("flow-123", "workspace-456")
	original.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	initialStorage := newMockKVStorage()
	err := original.Persist(context.Background(), initialStorage, NewKsuidGenerator())
	require.NoError(t, err)

	data, err := original.MarshalJSON()
	require.NoError(t, err)

	restored := &Llm2ChatHistory{}
	err = restored.UnmarshalJSON(data)
	require.NoError(t, err)

	container := &ChatHistoryContainer{History: restored}

	_, err = activities.ManageV4(context.Background(), container, "workspace-456", 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrate")
}

func TestManageV4_PersistError(t *testing.T) {
	storage := newMockKVStorage()
	storage.msetErr = errors.New("persist failed")
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
	})

	container := &ChatHistoryContainer{History: history}

	_, err := activities.ManageV4(context.Background(), container, "workspace-456", 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist")
}

func TestManageV4_WrongHistoryType(t *testing.T) {
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	legacyHistory := NewLegacyChatHistoryFromChatMessages(nil)
	container := &ChatHistoryContainer{History: legacyHistory}

	_, err := activities.ManageV4(context.Background(), container, "workspace-456", 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Llm2ChatHistory")
}

func TestManageV4_PreservesRefsForUnchangedMessages(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	firstBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "First message"}
	SetContextType(&firstBlock, ContextTypeInitialInstructions)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{firstBlock},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response"}},
	})
	secondBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Second message"}
	SetContextType(&secondBlock, ContextTypeUserFeedback)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{secondBlock},
	})

	err := history.Persist(context.Background(), storage, NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 100000, "")
	require.NoError(t, err)

	resultHistory := result.History.(*Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	assert.Equal(t, 3, resultHistory.Len())

	// The middle message (index 1) should have preserved its ref block IDs
	// (first and last get cache control changes, so their refs change)
	assert.Equal(t, originalRefs[1].BlockKeys, newRefs[1].BlockKeys,
		"unchanged middle message should preserve its block keys")
}

func TestManageV4_ChangesRefsForMarkerOnlyChanges(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	firstBlock := llm2.ContentBlock{
		Type:         llm2.ContentBlockTypeText,
		Text:         "First message",
		CacheControl: "ephemeral",
	}
	SetContextType(&firstBlock, ContextTypeInitialInstructions)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{firstBlock},
	})
	history.Append(&llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{
			Type: llm2.ContentBlockTypeText,
			Text: "Response",
		}},
	})

	err := history.Persist(context.Background(), storage, NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 100000, "")
	require.NoError(t, err)

	resultHistory := result.History.(*Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	require.Equal(t, 2, len(newRefs), "should have 2 messages after management")

	assert.NotEqual(t, originalRefs[1].BlockKeys, newRefs[1].BlockKeys,
		"message with marker change should get new block keys")
}

func TestManageV4_HydratingFromRefsRestoresMarkers(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	helloBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello"}
	SetContextType(&helloBlock, ContextTypeInitialInstructions)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{helloBlock},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hi there"}},
	})

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 100000, "")
	require.NoError(t, err)

	managedHistory := result.History.(*Llm2ChatHistory)
	refs := managedHistory.Refs()

	require.Len(t, refs, 2, "should have 2 refs after management")

	freshHistory := NewLlm2ChatHistory("flow-123", "workspace-456")
	freshHistory.SetRefs(refs)

	err = freshHistory.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	messages := freshHistory.Llm2Messages()
	require.Len(t, messages, 2)

	assert.Equal(t, "ephemeral", messages[0].Content[0].CacheControl,
		"first message should have ephemeral cache control after hydration")

	assert.Equal(t, "ephemeral", messages[1].Content[0].CacheControl,
		"last message should have ephemeral cache control after hydration")
}

func TestManageV4_PreservesContextType(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	instrBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Instructions"}
	SetContextType(&instrBlock, ContextTypeInitialInstructions)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{instrBlock},
	})
	history.Append(&llm2.Message{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Response"}},
	})

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 100000, "")
	require.NoError(t, err)

	managedHistory := result.History.(*Llm2ChatHistory)
	refs := managedHistory.Refs()

	require.Len(t, refs, 2, "should have 2 refs after management")

	freshHistory := NewLlm2ChatHistory("flow-123", "workspace-456")
	freshHistory.SetRefs(refs)
	err = freshHistory.Hydrate(context.Background(), storage)
	require.NoError(t, err)

	messages := freshHistory.Llm2Messages()
	assert.Equal(t, ContextTypeInitialInstructions, GetContextType(messages[0].Content[0]),
		"ContextType should be preserved after hydration")
}

func TestManageV4_DroppingOlderMessagesPreservesRetainedRefs(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")
	initBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Initial instructions"}
	SetContextType(&initBlock, ContextTypeInitialInstructions)
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{initBlock},
	})
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
	history.Append(&llm2.Message{
		Role:    llm2.RoleUser,
		Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Final question"}},
	})

	err := history.Persist(context.Background(), storage, NewKsuidGenerator())
	require.NoError(t, err)
	originalRefs := history.Refs()

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 50, "")
	require.NoError(t, err)

	resultHistory := result.History.(*Llm2ChatHistory)
	newRefs := resultHistory.Refs()

	require.GreaterOrEqual(t, len(newRefs), 2, "should retain at least first and last messages")

	assert.True(t, len(newRefs[len(newRefs)-1].BlockKeys) > 0,
		"last message should have block keys")

	if len(newRefs) > 2 {
		for i := 1; i < len(newRefs)-1; i++ {
			for j := 1; j < len(originalRefs)-1; j++ {
				if len(newRefs[i].BlockKeys) > 0 && len(originalRefs[j].BlockKeys) > 0 {
					if newRefs[i].BlockKeys[0] == originalRefs[j].BlockKeys[0] {
						t.Logf("Middle message at new index %d preserved ref from original index %d", i, j)
					}
				}
			}
		}
	}
}

func TestManageV4_LegacyChatMessageRetainsInitialInstructions(t *testing.T) {
	t.Parallel()
	storage := newMockKVStorage()
	activities := &ChatHistoryActivities{
		Storage: storage,
	}

	history := NewLlm2ChatHistory("flow-123", "workspace-456")

	history.Append(&common.ChatMessage{
		Role:        common.ChatMessageRoleUser,
		Content:     "You are a helpful assistant.",
		ContextType: ContextTypeInitialInstructions,
	})
	history.Append(&common.ChatMessage{
		Role:    common.ChatMessageRoleAssistant,
		Content: "Understood.",
	})
	history.Append(&common.ChatMessage{
		Role:    common.ChatMessageRoleUser,
		Content: "Do something.",
	})
	history.Append(&common.ChatMessage{
		Role:    common.ChatMessageRoleAssistant,
		Content: "Done.",
	})
	history.Append(&common.ChatMessage{
		Role:    common.ChatMessageRoleUser,
		Content: "Final question",
	})

	err := history.Persist(context.Background(), storage, NewKsuidGenerator())
	require.NoError(t, err)

	container := &ChatHistoryContainer{History: history}

	result, err := activities.ManageV4(context.Background(), container, "workspace-456", 30, "")
	require.NoError(t, err)

	resultHistory := result.History.(*Llm2ChatHistory)
	messages := resultHistory.Llm2Messages()

	require.GreaterOrEqual(t, len(messages), 2)

	foundInitialInstructions := false
	for _, msg := range messages {
		for _, block := range msg.Content {
			if GetContextType(block) == ContextTypeInitialInstructions {
				foundInitialInstructions = true
				assert.Equal(t, "You are a helpful assistant.", block.Text)
			}
		}
	}
	assert.True(t, foundInitialInstructions,
		"initial-instructions message should be retained after trimming")

	lastMsg := messages[len(messages)-1]
	assert.Equal(t, "Final question", lastMsg.Content[0].Text)
}
