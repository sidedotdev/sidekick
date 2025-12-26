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

// MessageRef stores a reference to a message's content blocks in KV storage.
type MessageRef struct {
	FlowId   string   `json:"flowId"`
	BlockIds []string `json:"blockIds"`
	Role     string   `json:"role"`
}

// Llm2ChatHistoryFactory creates a ChatHistory from MessageRefs.
// This is set by temp_common2 package at init time to avoid import cycles.
var Llm2ChatHistoryFactory func(refs []MessageRef) ChatHistory

// ChatHistory is an interface for managing chat message history.
type ChatHistory interface {
	Append(msg Message)
	Len() int
	Get(index int) Message
	Messages() []Message
	IsHydrated() bool
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

func (h *LegacyChatHistory) IsHydrated() bool {
	return true
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
	// Detect Llm2 format by checking for {"type": "llm2", ...} wrapper object
	if isLlm2Format(data) && Llm2ChatHistoryFactory != nil {
		var wrapper struct {
			Refs []MessageRef `json:"refs"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return err
		}
		c.History = Llm2ChatHistoryFactory(wrapper.Refs)
		return nil
	}

	// Fall back to legacy []ChatMessage format
	var msgs []ChatMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		return err
	}
	c.History = NewLegacyChatHistoryFromChatMessages(msgs)
	return nil
}

// isLlm2Format checks if the JSON data represents the Llm2ChatHistory wrapper format
// by looking for {"type": "llm2", ...} structure.
func isLlm2Format(data []byte) bool {
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	return obj.Type == "llm2"
}

func (c *ChatHistoryContainer) MarshalJSON() ([]byte, error) {
	if c.History == nil {
		return json.Marshal([]ChatMessage{})
	}
	return json.Marshal(c.History)
}

// Append adds a message to the underlying chat history.
func (c *ChatHistoryContainer) Append(msg Message) {
	if c.History == nil {
		c.History = NewLegacyChatHistoryFromChatMessages(nil)
	}
	c.History.Append(msg)
}

// Len returns the number of messages in the underlying chat history.
func (c *ChatHistoryContainer) Len() int {
	if c.History == nil {
		return 0
	}
	return c.History.Len()
}

// Get returns the message at the given index from the underlying chat history.
func (c *ChatHistoryContainer) Get(index int) Message {
	if c.History == nil {
		return nil
	}
	return c.History.Get(index)
}

// Messages returns all messages from the underlying chat history.
func (c *ChatHistoryContainer) Messages() []Message {
	if c.History == nil {
		return nil
	}
	return c.History.Messages()
}

// Hydrate hydrates the underlying chat history from storage.
func (c *ChatHistoryContainer) Hydrate(ctx context.Context, storage KeyValueStorage) error {
	if c.History == nil {
		return nil
	}
	return c.History.Hydrate(ctx, storage)
}

// Persist persists the underlying chat history to storage.
func (c *ChatHistoryContainer) Persist(ctx context.Context, storage KeyValueStorage) error {
	if c.History == nil {
		return nil
	}
	return c.History.Persist(ctx, storage)
}

// IsHydrated returns whether the underlying chat history is hydrated.
func (c *ChatHistoryContainer) IsHydrated() bool {
	if c.History == nil {
		return true
	}
	return c.History.IsHydrated()
}
