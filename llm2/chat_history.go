package llm2

import (
	"context"
	"encoding/json"
	"fmt"

	"sidekick/common"

	"github.com/segmentio/ksuid"
)

// BlockIdGenerator is a function that generates unique block IDs.
// For workflow code, this should be backed by a deterministic side effect.
// For non-workflow code, this can use ksuid.New().String() directly.
type BlockIdGenerator func() string

// NewKsuidGenerator returns a BlockIdGenerator that uses ksuid.New().
func NewKsuidGenerator() BlockIdGenerator {
	return func() string {
		return ksuid.New().String()
	}
}

// MessageRef stores a reference to a message's content blocks in KV storage.
type MessageRef struct {
	BlockIds []string `json:"blockIds"`
	Role     string   `json:"role"`
}

// ChatHistory is an interface for managing chat message history.
type ChatHistory interface {
	Append(msg common.Message)
	Len() int
	Get(index int) common.Message
	Set(index int, msg common.Message)
	Messages() []common.Message
	IsHydrated() bool
	Hydrate(ctx context.Context, storage common.KeyValueStorage) error
	Persist(ctx context.Context, storage common.KeyValueStorage, gen BlockIdGenerator) error
}

// LegacyChatHistory wraps a slice of ChatMessage to implement ChatHistory.
// It provides backward compatibility with existing code that uses []ChatMessage.
type LegacyChatHistory struct {
	messages []common.ChatMessage
}

// NewLegacyChatHistoryFromChatMessages creates a LegacyChatHistory from a slice of ChatMessage.
func NewLegacyChatHistoryFromChatMessages(msgs []common.ChatMessage) *LegacyChatHistory {
	return &LegacyChatHistory{messages: msgs}
}

func (h *LegacyChatHistory) Append(msg common.Message) {
	if cm, ok := msg.(common.ChatMessage); ok {
		h.messages = append(h.messages, cm)
	} else if cmp, ok := msg.(*common.ChatMessage); ok {
		h.messages = append(h.messages, *cmp)
	}
}

func (h *LegacyChatHistory) Len() int {
	return len(h.messages)
}

func (h *LegacyChatHistory) Get(index int) common.Message {
	if index < 0 || index >= len(h.messages) {
		return nil
	}
	return h.messages[index]
}

func (h *LegacyChatHistory) Set(index int, msg common.Message) {
	if index < 0 || index >= len(h.messages) {
		return
	}
	if cm, ok := msg.(common.ChatMessage); ok {
		h.messages[index] = cm
	} else if cmp, ok := msg.(*common.ChatMessage); ok {
		h.messages[index] = *cmp
	}
}

func (h *LegacyChatHistory) Messages() []common.Message {
	result := make([]common.Message, len(h.messages))
	for i, msg := range h.messages {
		result[i] = msg
	}
	return result
}

func (h *LegacyChatHistory) IsHydrated() bool {
	return true
}

func (h *LegacyChatHistory) Hydrate(ctx context.Context, storage common.KeyValueStorage) error {
	return nil
}

func (h *LegacyChatHistory) Persist(ctx context.Context, storage common.KeyValueStorage, gen BlockIdGenerator) error {
	return nil
}

func (h *LegacyChatHistory) MarshalJSON() ([]byte, error) {
	if h.messages == nil {
		return json.Marshal([]common.ChatMessage{})
	}
	return json.Marshal(h.messages)
}

// Llm2ChatHistory stores message references in Temporal history and persists
// actual content to KV storage for deduplication.
type Llm2ChatHistory struct {
	flowId         string
	workspaceId    string
	refs           []MessageRef
	messages       []Message
	hydrated       bool
	unpersisted    []int
	hydratedBlocks map[string]ContentBlock
}

// NewLlm2ChatHistory creates an empty, hydrated Llm2ChatHistory instance.
func NewLlm2ChatHistory(flowId, workspaceId string) *Llm2ChatHistory {
	return &Llm2ChatHistory{
		flowId:      flowId,
		workspaceId: workspaceId,
		refs:        []MessageRef{},
		messages:    []Message{},
		hydrated:    true,
		unpersisted: []int{},
	}
}

// newLlm2ChatHistoryFromRefs creates an Llm2ChatHistory from refs (used during unmarshal).
func newLlm2ChatHistoryFromRefs(refs []MessageRef) *Llm2ChatHistory {
	h := &Llm2ChatHistory{
		refs:        refs,
		unpersisted: []int{},
	}
	if len(refs) == 0 {
		h.hydrated = true
		h.messages = []Message{}
	} else {
		h.hydrated = false
		h.messages = nil
	}
	return h
}

func (h *Llm2ChatHistory) Append(msg common.Message) {
	if !h.hydrated {
		panic("cannot append to non-hydrated Llm2ChatHistory")
	}
	if mp, ok := msg.(*Message); ok {
		h.messages = append(h.messages, *mp)
		h.unpersisted = append(h.unpersisted, len(h.messages)-1)
	} else if cm, ok := msg.(common.ChatMessage); ok {
		h.messages = append(h.messages, MessageFromChatMessage(cm))
		h.unpersisted = append(h.unpersisted, len(h.messages)-1)
	}
}

func MessageFromChatMessage(cm common.ChatMessage) Message {
	// Tool results in legacy format have role "tool", but in llm2 they should
	// be user-role messages with a ToolResult content block
	if cm.Role == common.ChatMessageRoleTool {
		return Message{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: ContentBlockTypeToolResult,
				ToolResult: &ToolResultBlock{
					ToolCallId: cm.ToolCallId,
					Name:       cm.Name,
					IsError:    cm.IsError,
					Text:       cm.Content,
				},
			}},
		}
	}
	return Message{
		Role:    Role(cm.Role),
		Content: []ContentBlock{{Type: ContentBlockTypeText, Text: cm.Content}},
	}
}

func (h *Llm2ChatHistory) Len() int {
	if !h.hydrated {
		return len(h.refs)
	}
	return len(h.messages)
}

func (h *Llm2ChatHistory) Get(index int) common.Message {
	if !h.hydrated {
		panic("cannot get message from non-hydrated Llm2ChatHistory")
	}
	if index < 0 || index >= len(h.messages) {
		return nil
	}
	return &h.messages[index]
}

func (h *Llm2ChatHistory) Set(index int, msg common.Message) {
	if !h.hydrated {
		panic("cannot set message in non-hydrated Llm2ChatHistory")
	}
	if index < 0 || index >= len(h.messages) {
		return
	}
	if mp, ok := msg.(*Message); ok {
		h.messages[index] = *mp
		h.unpersisted = append(h.unpersisted, index)
	}
}

func (h *Llm2ChatHistory) Messages() []common.Message {
	if !h.hydrated {
		panic("cannot get messages from non-hydrated Llm2ChatHistory")
	}
	result := make([]common.Message, len(h.messages))
	for i := range h.messages {
		result[i] = &h.messages[i]
	}
	return result
}

// Llm2Messages returns the underlying Message slice directly.
func (h *Llm2ChatHistory) Llm2Messages() []Message {
	if !h.hydrated {
		panic("cannot get llm2 messages from non-hydrated Llm2ChatHistory")
	}
	return h.messages
}

func (h *Llm2ChatHistory) Hydrate(ctx context.Context, storage common.KeyValueStorage) error {
	if h.hydrated {
		return nil
	}

	// Initialize cache if needed
	if h.hydratedBlocks == nil {
		h.hydratedBlocks = make(map[string]ContentBlock)
	}

	// Populate cache from existing in-memory messages+refs before overwriting
	if h.messages != nil && len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			if i < len(h.messages) {
				msg := h.messages[i]
				for j, blockId := range ref.BlockIds {
					if j < len(msg.Content) {
						h.hydratedBlocks[blockId] = msg.Content[j]
					}
				}
			}
		}
	}

	// Collect missing block IDs to fetch
	var missingKeys []string
	for _, ref := range h.refs {
		for _, blockId := range ref.BlockIds {
			if _, ok := h.hydratedBlocks[blockId]; !ok {
				missingKeys = append(missingKeys, blockId)
			}
		}
	}

	if len(missingKeys) == 0 && len(h.refs) == 0 {
		h.messages = []Message{}
		h.hydrated = true
		return nil
	}

	// Fetch only missing content blocks
	if len(missingKeys) > 0 {
		values, err := storage.MGet(ctx, h.workspaceId, missingKeys)
		if err != nil {
			return fmt.Errorf("failed to fetch content blocks: %w", err)
		}

		for i, key := range missingKeys {
			if values[i] == nil {
				continue
			}
			var block ContentBlock
			if err := json.Unmarshal(values[i], &block); err != nil {
				return fmt.Errorf("failed to unmarshal content block %s: %w", key, err)
			}
			h.hydratedBlocks[key] = block
		}
	}

	// Reconstruct messages from refs using the cache
	h.messages = make([]Message, len(h.refs))
	for i, ref := range h.refs {
		h.messages[i] = Message{
			Role:    Role(ref.Role),
			Content: make([]ContentBlock, 0, len(ref.BlockIds)),
		}
		for _, blockId := range ref.BlockIds {
			if block, ok := h.hydratedBlocks[blockId]; ok {
				h.messages[i].Content = append(h.messages[i].Content, block)
			}
		}
	}

	h.hydrated = true
	h.unpersisted = []int{}
	return nil
}

func (h *Llm2ChatHistory) IsHydrated() bool {
	return h.hydrated
}

func (h *Llm2ChatHistory) Persist(ctx context.Context, storage common.KeyValueStorage, gen BlockIdGenerator) error {
	if !h.hydrated {
		return fmt.Errorf("cannot persist non-hydrated Llm2ChatHistory")
	}

	if len(h.unpersisted) == 0 {
		return nil
	}

	// Initialize cache if needed
	if h.hydratedBlocks == nil {
		h.hydratedBlocks = make(map[string]ContentBlock)
	}

	// Ensure refs slice is large enough
	for len(h.refs) < len(h.messages) {
		h.refs = append(h.refs, MessageRef{})
	}

	// Collect all content blocks to persist
	values := make(map[string][]byte)
	for _, idx := range h.unpersisted {
		if idx < 0 || idx >= len(h.messages) {
			continue
		}
		msg := h.messages[idx]
		blockIds := make([]string, len(msg.Content))
		for j, block := range msg.Content {
			blockId := fmt.Sprintf("%s:msg:%s", h.flowId, gen())
			blockIds[j] = blockId
			blockBytes, err := json.Marshal(block)
			if err != nil {
				return fmt.Errorf("failed to marshal content block: %w", err)
			}
			values[blockId] = blockBytes
			h.hydratedBlocks[blockId] = block
		}
		h.refs[idx] = MessageRef{
			BlockIds: blockIds,
			Role:     string(msg.Role),
		}
	}

	if len(values) > 0 {
		if err := storage.MSetRaw(ctx, h.workspaceId, values); err != nil {
			return fmt.Errorf("failed to persist content blocks: %w", err)
		}
	}

	h.unpersisted = []int{}
	return nil
}

// llm2ChatHistoryJSON is the wrapper format for Llm2ChatHistory serialization.
type llm2ChatHistoryJSON struct {
	Type        string       `json:"type"`
	Refs        []MessageRef `json:"refs"`
	FlowId      string       `json:"flowId,omitempty"`
	WorkspaceId string       `json:"workspaceId,omitempty"`
}

func (h *Llm2ChatHistory) MarshalJSON() ([]byte, error) {
	return json.Marshal(llm2ChatHistoryJSON{
		Type:        "llm2",
		Refs:        h.refs,
		FlowId:      h.flowId,
		WorkspaceId: h.workspaceId,
	})
}

func (h *Llm2ChatHistory) UnmarshalJSON(data []byte) error {
	var wrapper llm2ChatHistoryJSON
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	h.refs = wrapper.Refs
	h.flowId = wrapper.FlowId
	h.workspaceId = wrapper.WorkspaceId
	h.unpersisted = []int{}
	if len(wrapper.Refs) == 0 {
		h.hydrated = true
		h.messages = []Message{}
	} else {
		h.hydrated = false
		h.messages = nil
	}
	return nil
}

// SetFlowId sets the flow ID for the chat history.
func (h *Llm2ChatHistory) SetFlowId(flowId string) {
	h.flowId = flowId
}

// SetWorkspaceId sets the workspace ID for the chat history.
func (h *Llm2ChatHistory) SetWorkspaceId(workspaceId string) {
	h.workspaceId = workspaceId
}

// Refs returns a defensive copy of the message refs.
func (h *Llm2ChatHistory) Refs() []MessageRef {
	result := make([]MessageRef, len(h.refs))
	for i, ref := range h.refs {
		result[i] = MessageRef{
			BlockIds: append([]string(nil), ref.BlockIds...),
			Role:     ref.Role,
		}
	}
	return result
}

// SetRefs replaces the refs and marks the history as non-hydrated.
// The existing in-memory messages are preserved in the hydratedBlocks cache
// so that subsequent Hydrate calls can reuse already-fetched content.
func (h *Llm2ChatHistory) SetRefs(refs []MessageRef) {
	// Populate cache from existing messages+refs before changing refs
	if h.hydratedBlocks == nil {
		h.hydratedBlocks = make(map[string]ContentBlock)
	}
	if h.hydrated && h.messages != nil && len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			if i < len(h.messages) {
				msg := h.messages[i]
				for j, blockId := range ref.BlockIds {
					if j < len(msg.Content) {
						h.hydratedBlocks[blockId] = msg.Content[j]
					}
				}
			}
		}
	}

	h.refs = refs
	h.hydrated = false
	h.messages = nil
	h.unpersisted = []int{}
}

// SetMessages replaces all messages in the history with the provided slice.
// All messages are marked as unpersisted.
func (h *Llm2ChatHistory) SetMessages(messages []Message) {
	if !h.hydrated {
		panic("cannot set messages on non-hydrated Llm2ChatHistory")
	}
	h.messages = messages
	h.refs = make([]MessageRef, len(messages))
	h.unpersisted = make([]int, len(messages))
	for i := range messages {
		h.unpersisted[i] = i
	}
}

// SetUnpersisted sets the list of message indices that need to be persisted.
// This allows callers to narrow persistence to only changed messages.
func (h *Llm2ChatHistory) SetUnpersisted(indices []int) {
	h.unpersisted = indices
}

// SetHydratedWithMessages sets the messages and marks the history as hydrated.
// Unlike SetMessages, this does not reset refs or mark messages as unpersisted.
// This is useful when the caller has already set refs via SetRefs and wants to
// restore the hydrated state with known messages.
func (h *Llm2ChatHistory) SetHydratedWithMessages(messages []Message) {
	h.messages = messages
	h.hydrated = true

	// Populate the cache from the new messages and current refs
	if h.hydratedBlocks == nil {
		h.hydratedBlocks = make(map[string]ContentBlock)
	}
	if len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			msg := h.messages[i]
			for j, blockId := range ref.BlockIds {
				if j < len(msg.Content) {
					h.hydratedBlocks[blockId] = msg.Content[j]
				}
			}
		}
	}
}

// ChatHistoryContainer wraps a ChatHistory for JSON serialization.
// It handles detection of the underlying format during unmarshaling.
type ChatHistoryContainer struct {
	History ChatHistory
}

func (c *ChatHistoryContainer) UnmarshalJSON(data []byte) error {
	// Detect Llm2 format by checking for {"type": "llm2", ...} wrapper object
	if isLlm2Format(data) {
		var wrapper struct {
			Refs        []MessageRef `json:"refs"`
			FlowId      string       `json:"flowId,omitempty"`
			WorkspaceId string       `json:"workspaceId,omitempty"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return err
		}
		h := newLlm2ChatHistoryFromRefs(wrapper.Refs)
		h.flowId = wrapper.FlowId
		h.workspaceId = wrapper.WorkspaceId
		c.History = h
		return nil
	}

	// Fall back to legacy []ChatMessage format
	var msgs []common.ChatMessage
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
		return json.Marshal([]common.ChatMessage{})
	}
	return json.Marshal(c.History)
}

// Append adds a message to the underlying chat history.
func (c *ChatHistoryContainer) Append(msg common.Message) {
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
func (c *ChatHistoryContainer) Get(index int) common.Message {
	if c.History == nil {
		return nil
	}
	return c.History.Get(index)
}

func (c *ChatHistoryContainer) Set(index int, msg common.Message) {
	if c.History == nil {
		return
	}
	c.History.Set(index, msg)
}

// Messages returns all messages from the underlying chat history.
func (c *ChatHistoryContainer) Messages() []common.Message {
	if c.History == nil {
		return nil
	}
	return c.History.Messages()
}

// Hydrate hydrates the underlying chat history from storage.
func (c *ChatHistoryContainer) Hydrate(ctx context.Context, storage common.KeyValueStorage) error {
	if c.History == nil {
		return nil
	}
	return c.History.Hydrate(ctx, storage)
}

// Persist persists the underlying chat history to storage.
func (c *ChatHistoryContainer) Persist(ctx context.Context, storage common.KeyValueStorage, gen BlockIdGenerator) error {
	if c.History == nil {
		return nil
	}
	return c.History.Persist(ctx, storage, gen)
}

// IsHydrated returns whether the underlying chat history is hydrated.
func (c *ChatHistoryContainer) IsHydrated() bool {
	if c.History == nil {
		return true
	}
	return c.History.IsHydrated()
}

// Llm2Messages returns the underlying []Message if the history is an Llm2ChatHistory,
// otherwise returns nil.
func (c *ChatHistoryContainer) Llm2Messages() []Message {
	if c == nil || c.History == nil {
		return nil
	}
	if llm2History, ok := c.History.(*Llm2ChatHistory); ok {
		return llm2History.Llm2Messages()
	}
	return nil
}
