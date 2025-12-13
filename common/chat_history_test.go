package common

import (
	"context"
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
