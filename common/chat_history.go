package common

import (
	"context"
	"encoding/json"
)

// KeyValueStorage provides key-value storage operations.
// This interface is duplicated from srv.KeyValueStorage to avoid import cycles.
type KeyValueStorage interface {
	MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)
	MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error
}

// ChatHistory is an interface for managing chat message history.
type ChatHistory interface {
	Append(msg Message)
	Len() int
	Get(index int) Message
	Messages() []Message
	Hydrate(ctx context.Context, storage KeyValueStorage) error
	Persist(ctx context.Context, storage KeyValueStorage) error
}

// LegacyChatHistory wraps a slice of ChatMessage to implement ChatHistory.
// It provides backward compatibility with existing code that uses []ChatMessage.
type LegacyChatHistory struct {
	messages []ChatMessage
}

// NewLegacyChatHistoryFromChatMessages creates a LegacyChatHistory from a slice of ChatMessage.
func NewLegacyChatHistoryFromChatMessages(msgs []ChatMessage) *LegacyChatHistory {
	return &LegacyChatHistory{messages: msgs}
}

func (h *LegacyChatHistory) Append(msg Message) {
	if cm, ok := msg.(ChatMessage); ok {
		h.messages = append(h.messages, cm)
	} else if cmp, ok := msg.(*ChatMessage); ok {
		h.messages = append(h.messages, *cmp)
	}
}

func (h *LegacyChatHistory) Len() int {
	return len(h.messages)
}

func (h *LegacyChatHistory) Get(index int) Message {
	if index < 0 || index >= len(h.messages) {
		return nil
	}
	return h.messages[index]
}

func (h *LegacyChatHistory) Messages() []Message {
	result := make([]Message, len(h.messages))
	for i, msg := range h.messages {
		result[i] = msg
	}
	return result
}

func (h *LegacyChatHistory) Hydrate(ctx context.Context, storage KeyValueStorage) error {
	return nil
}

func (h *LegacyChatHistory) Persist(ctx context.Context, storage KeyValueStorage) error {
	return nil
}

func (h *LegacyChatHistory) MarshalJSON() ([]byte, error) {
	if h.messages == nil {
		return json.Marshal([]ChatMessage{})
	}
	return json.Marshal(h.messages)
}

// ChatHistoryContainer wraps a ChatHistory for JSON serialization.
// It handles detection of the underlying format during unmarshaling.
type ChatHistoryContainer struct {
	History ChatHistory
}

func (c *ChatHistoryContainer) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as []ChatMessage (legacy format)
	var msgs []ChatMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		return err
	}
	c.History = NewLegacyChatHistoryFromChatMessages(msgs)
	return nil
}

func (c *ChatHistoryContainer) MarshalJSON() ([]byte, error) {
	if c.History == nil {
		return json.Marshal([]ChatMessage{})
	}
	return json.Marshal(c.History)
}
