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
	err := original.Persist(context.Background(), storage)
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
	err := original.Persist(context.Background(), initialStorage)
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
