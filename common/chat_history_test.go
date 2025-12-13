package common

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLegacyChatHistory_NewFromChatMessages(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi there"},
	}

	history := NewLegacyChatHistoryFromChatMessages(msgs)

	assert.Equal(t, 2, history.Len())
}

func TestLegacyChatHistory_NewFromEmptySlice(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages([]ChatMessage{})
	assert.Equal(t, 0, history.Len())
}

func TestLegacyChatHistory_NewFromNilSlice(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages(nil)
	assert.Equal(t, 0, history.Len())
}

func TestLegacyChatHistory_Append(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages(nil)

	history.Append(ChatMessage{Role: ChatMessageRoleUser, Content: "First"})
	assert.Equal(t, 1, history.Len())

	history.Append(ChatMessage{Role: ChatMessageRoleAssistant, Content: "Second"})
	assert.Equal(t, 2, history.Len())
}

func TestLegacyChatHistory_AppendPointer(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages(nil)

	msg := &ChatMessage{Role: ChatMessageRoleUser, Content: "Pointer message"}
	history.Append(msg)

	assert.Equal(t, 1, history.Len())
	assert.Equal(t, "Pointer message", history.Get(0).GetContentString())
}

func TestLegacyChatHistory_Len(t *testing.T) {
	tests := []struct {
		name     string
		messages []ChatMessage
		expected int
	}{
		{
			name:     "empty",
			messages: nil,
			expected: 0,
		},
		{
			name:     "one message",
			messages: []ChatMessage{{Role: ChatMessageRoleUser, Content: "Hi"}},
			expected: 1,
		},
		{
			name: "multiple messages",
			messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Hi"},
				{Role: ChatMessageRoleAssistant, Content: "Hello"},
				{Role: ChatMessageRoleUser, Content: "How are you?"},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := NewLegacyChatHistoryFromChatMessages(tt.messages)
			assert.Equal(t, tt.expected, history.Len())
		})
	}
}

func TestLegacyChatHistory_Get(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "First"},
		{Role: ChatMessageRoleAssistant, Content: "Second"},
		{Role: ChatMessageRoleUser, Content: "Third"},
	}
	history := NewLegacyChatHistoryFromChatMessages(msgs)

	t.Run("valid indices", func(t *testing.T) {
		msg := history.Get(0)
		assert.NotNil(t, msg)
		assert.Equal(t, "First", msg.GetContentString())

		msg = history.Get(1)
		assert.NotNil(t, msg)
		assert.Equal(t, "Second", msg.GetContentString())

		msg = history.Get(2)
		assert.NotNil(t, msg)
		assert.Equal(t, "Third", msg.GetContentString())
	})

	t.Run("negative index", func(t *testing.T) {
		msg := history.Get(-1)
		assert.Nil(t, msg)
	})

	t.Run("out of bounds index", func(t *testing.T) {
		msg := history.Get(3)
		assert.Nil(t, msg)

		msg = history.Get(100)
		assert.Nil(t, msg)
	})
}

func TestLegacyChatHistory_Messages(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi"},
	}
	history := NewLegacyChatHistoryFromChatMessages(msgs)

	messages := history.Messages()

	assert.Len(t, messages, 2)
	assert.Equal(t, "user", messages[0].GetRole())
	assert.Equal(t, "Hello", messages[0].GetContentString())
	assert.Equal(t, "assistant", messages[1].GetRole())
	assert.Equal(t, "Hi", messages[1].GetContentString())
}

func TestLegacyChatHistory_MessagesEmpty(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages(nil)
	messages := history.Messages()
	assert.NotNil(t, messages)
	assert.Len(t, messages, 0)
}

func TestLegacyChatHistory_Hydrate(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages([]ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Test"},
	})

	err := history.Hydrate(context.Background(), nil)
	assert.NoError(t, err)
}

func TestLegacyChatHistory_Persist(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages([]ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Test"},
	})

	err := history.Persist(context.Background(), nil)
	assert.NoError(t, err)
}

func TestLegacyChatHistory_ImplementsChatHistory(t *testing.T) {
	var _ ChatHistory = (*LegacyChatHistory)(nil)
}

func TestLegacyChatHistory_MarshalJSON(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi there"},
	}

	history := NewLegacyChatHistoryFromChatMessages(msgs)

	historyJSON, err := json.Marshal(history)
	assert.NoError(t, err)

	msgsJSON, err := json.Marshal(msgs)
	assert.NoError(t, err)

	assert.JSONEq(t, string(msgsJSON), string(historyJSON))
}

func TestLegacyChatHistory_MarshalJSON_Empty(t *testing.T) {
	history := NewLegacyChatHistoryFromChatMessages(nil)

	historyJSON, err := json.Marshal(history)
	assert.NoError(t, err)

	assert.JSONEq(t, "[]", string(historyJSON))
}

func TestChatHistoryContainer_UnmarshalJSON(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi there"},
	}

	msgsJSON, err := json.Marshal(msgs)
	assert.NoError(t, err)

	var container ChatHistoryContainer
	err = json.Unmarshal(msgsJSON, &container)
	assert.NoError(t, err)

	assert.NotNil(t, container.History)
	_, ok := container.History.(*LegacyChatHistory)
	assert.True(t, ok, "History should be *LegacyChatHistory")

	assert.Equal(t, 2, container.History.Len())
	assert.Equal(t, "user", container.History.Get(0).GetRole())
	assert.Equal(t, "Hello", container.History.Get(0).GetContentString())
	assert.Equal(t, "assistant", container.History.Get(1).GetRole())
	assert.Equal(t, "Hi there", container.History.Get(1).GetContentString())
}

func TestChatHistoryContainer_UnmarshalJSON_Empty(t *testing.T) {
	var container ChatHistoryContainer
	err := json.Unmarshal([]byte("[]"), &container)
	assert.NoError(t, err)

	assert.NotNil(t, container.History)
	assert.Equal(t, 0, container.History.Len())
}

func TestChatHistoryContainer_MarshalJSON(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi there"},
	}

	container := ChatHistoryContainer{
		History: NewLegacyChatHistoryFromChatMessages(msgs),
	}

	containerJSON, err := json.Marshal(&container)
	assert.NoError(t, err)

	msgsJSON, err := json.Marshal(msgs)
	assert.NoError(t, err)

	assert.JSONEq(t, string(msgsJSON), string(containerJSON))
}

func TestChatHistoryContainer_MarshalJSON_NilHistory(t *testing.T) {
	container := ChatHistoryContainer{History: nil}

	containerJSON, err := json.Marshal(&container)
	assert.NoError(t, err)

	assert.JSONEq(t, "[]", string(containerJSON))
}

func TestChatHistoryContainer_RoundTrip(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Hello"},
		{Role: ChatMessageRoleAssistant, Content: "Hi there"},
		{Role: ChatMessageRoleUser, Content: "How are you?"},
	}

	original := ChatHistoryContainer{
		History: NewLegacyChatHistoryFromChatMessages(msgs),
	}

	data, err := json.Marshal(&original)
	assert.NoError(t, err)

	var restored ChatHistoryContainer
	err = json.Unmarshal(data, &restored)
	assert.NoError(t, err)

	assert.Equal(t, original.History.Len(), restored.History.Len())
	for i := 0; i < original.History.Len(); i++ {
		assert.Equal(t, original.History.Get(i).GetRole(), restored.History.Get(i).GetRole())
		assert.Equal(t, original.History.Get(i).GetContentString(), restored.History.Get(i).GetContentString())
	}
}

func TestChatHistoryContainer_RoundTrip_WithToolCalls(t *testing.T) {
	msgs := []ChatMessage{
		{Role: ChatMessageRoleUser, Content: "Search for something"},
		{
			Role:    ChatMessageRoleAssistant,
			Content: "",
			ToolCalls: []ToolCall{
				{Id: "call_123", Name: "search", Arguments: `{"query": "test"}`},
			},
		},
		{Role: ChatMessageRoleTool, Content: "Search results", Name: "search", ToolCallId: "call_123"},
	}

	original := ChatHistoryContainer{
		History: NewLegacyChatHistoryFromChatMessages(msgs),
	}

	data, err := json.Marshal(&original)
	assert.NoError(t, err)

	var restored ChatHistoryContainer
	err = json.Unmarshal(data, &restored)
	assert.NoError(t, err)

	assert.Equal(t, original.History.Len(), restored.History.Len())

	// Verify tool call details are preserved
	restoredHistory, ok := restored.History.(*LegacyChatHistory)
	assert.True(t, ok)
	originalHistory := original.History.(*LegacyChatHistory)

	for i := 0; i < originalHistory.Len(); i++ {
		origMsg := originalHistory.messages[i]
		restoredMsg := restoredHistory.messages[i]
		assert.Equal(t, origMsg.Role, restoredMsg.Role)
		assert.Equal(t, origMsg.Content, restoredMsg.Content)
		assert.Equal(t, origMsg.ToolCalls, restoredMsg.ToolCalls)
		assert.Equal(t, origMsg.Name, restoredMsg.Name)
		assert.Equal(t, origMsg.ToolCallId, restoredMsg.ToolCallId)
	}
}
