// Package temp_common2 contains Llm2ChatHistory.
//
// IMPORTANT: This type cannot be placed in the common package due to Go's import
// cycle restrictions. The llm2 package imports common to use common.ToolCall (for
// GetToolCalls/SetToolCalls methods) and common.Message interface. If common were
// to import llm2 (to reference llm2.Message), it would create a circular dependency:
//
//	common -> llm2 -> common (cycle!)
//
// Therefore, Llm2ChatHistory must live in a separate package that can import both
// common and llm2 without creating cycles. This is a fundamental Go constraint,
// not a design oversight. The original requirements specified common/chat_history.go
// but that is architecturally impossible given the existing package dependencies.
package temp_common2

import (
	"context"
	"encoding/json"
	"fmt"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/segmentio/ksuid"
)

func init() {
	common.Llm2ChatHistoryFactory = func(refs []common.MessageRef) common.ChatHistory {
		h := &Llm2ChatHistory{
			refs:        refs,
			hydrated:    false,
			messages:    nil,
			unpersisted: []int{},
		}
		return h
	}
}

// Llm2ChatHistory stores message references in Temporal history and persists
// actual content to KV storage for deduplication.
type Llm2ChatHistory struct {
	flowId      string
	workspaceId string
	refs        []common.MessageRef
	messages    []llm2.Message
	hydrated    bool
	unpersisted []int
}

// NewLlm2ChatHistory creates an empty, hydrated Llm2ChatHistory instance.
func NewLlm2ChatHistory(flowId, workspaceId string) *Llm2ChatHistory {
	return &Llm2ChatHistory{
		flowId:      flowId,
		workspaceId: workspaceId,
		refs:        []common.MessageRef{},
		messages:    []llm2.Message{},
		hydrated:    true,
		unpersisted: []int{},
	}
}

func (h *Llm2ChatHistory) Append(msg common.Message) {
	if !h.hydrated {
		panic("cannot append to non-hydrated Llm2ChatHistory")
	}
	if mp, ok := msg.(*llm2.Message); ok {
		h.messages = append(h.messages, *mp)
		h.unpersisted = append(h.unpersisted, len(h.messages)-1)
	}
}

func (h *Llm2ChatHistory) Len() int {
	if !h.hydrated {
		panic("cannot get length of non-hydrated Llm2ChatHistory")
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
	// Return pointer to satisfy common.Message interface (SetToolCalls has pointer receiver)
	return &h.messages[index]
}

func (h *Llm2ChatHistory) Set(index int, msg common.Message) {
	if !h.hydrated {
		panic("cannot set message in non-hydrated Llm2ChatHistory")
	}
	if index < 0 || index >= len(h.messages) {
		return
	}
	if mp, ok := msg.(*llm2.Message); ok {
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
		// Return pointers to satisfy common.Message interface (SetToolCalls has pointer receiver)
		result[i] = &h.messages[i]
	}
	return result
}

// Llm2Messages returns the underlying llm2.Message slice directly.
func (h *Llm2ChatHistory) Llm2Messages() []llm2.Message {
	if !h.hydrated {
		panic("cannot get messages from non-hydrated Llm2ChatHistory")
	}
	return h.messages
}

func (h *Llm2ChatHistory) Hydrate(ctx context.Context, storage common.KeyValueStorage) error {
	if h.hydrated {
		return nil
	}

	// Restore flowId from refs if not already set
	if h.flowId == "" && len(h.refs) > 0 {
		h.flowId = h.refs[0].FlowId
	}

	// Collect all block IDs to fetch
	var allKeys []string
	for _, ref := range h.refs {
		allKeys = append(allKeys, ref.BlockIds...)
	}

	if len(allKeys) == 0 {
		h.messages = []llm2.Message{}
		h.hydrated = true
		return nil
	}

	// Fetch all content blocks
	values, err := storage.MGet(ctx, h.workspaceId, allKeys)
	if err != nil {
		return fmt.Errorf("failed to fetch content blocks: %w", err)
	}

	// Build a map of block ID to content block
	blockMap := make(map[string]llm2.ContentBlock)
	for i, key := range allKeys {
		if values[i] == nil {
			continue
		}
		var block llm2.ContentBlock
		if err := json.Unmarshal(values[i], &block); err != nil {
			return fmt.Errorf("failed to unmarshal content block %s: %w", key, err)
		}
		blockMap[key] = block
	}

	// Reconstruct messages from refs
	h.messages = make([]llm2.Message, len(h.refs))
	for i, ref := range h.refs {
		h.messages[i] = llm2.Message{
			Role:    llm2.Role(ref.Role),
			Content: make([]llm2.ContentBlock, 0, len(ref.BlockIds)),
		}
		for _, blockId := range ref.BlockIds {
			if block, ok := blockMap[blockId]; ok {
				h.messages[i].Content = append(h.messages[i].Content, block)
			}
		}
	}

	h.hydrated = true
	h.unpersisted = []int{}
	return nil
}

func (h *Llm2ChatHistory) Persist(ctx context.Context, storage common.KeyValueStorage) error {
	if !h.hydrated {
		return fmt.Errorf("cannot persist non-hydrated Llm2ChatHistory")
	}

	if len(h.unpersisted) == 0 {
		return nil
	}

	// Ensure refs slice is large enough
	for len(h.refs) < len(h.messages) {
		h.refs = append(h.refs, common.MessageRef{})
	}

	// Collect all content blocks to persist
	values := make(map[string]interface{})
	for _, idx := range h.unpersisted {
		if idx < 0 || idx >= len(h.messages) {
			continue
		}
		msg := h.messages[idx]
		blockIds := make([]string, len(msg.Content))
		for j, block := range msg.Content {
			blockId := fmt.Sprintf("%s:msg:%d:block:%s", h.flowId, idx, ksuid.New().String())
			blockIds[j] = blockId
			values[blockId] = block
		}
		h.refs[idx] = common.MessageRef{
			FlowId:   h.flowId,
			BlockIds: blockIds,
			Role:     string(msg.Role),
		}
	}

	if len(values) > 0 {
		if err := storage.MSet(ctx, h.workspaceId, values); err != nil {
			return fmt.Errorf("failed to persist content blocks: %w", err)
		}
	}

	h.unpersisted = []int{}
	return nil
}

func (h *Llm2ChatHistory) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.refs)
}

func (h *Llm2ChatHistory) UnmarshalJSON(data []byte) error {
	var refs []common.MessageRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return err
	}
	h.refs = refs
	h.hydrated = false
	h.messages = nil
	h.unpersisted = []int{}
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

// SetMessages replaces all messages in the history with the provided slice.
// All messages are marked as unpersisted.
func (h *Llm2ChatHistory) SetMessages(messages []llm2.Message) {
	if !h.hydrated {
		panic("cannot set messages on non-hydrated Llm2ChatHistory")
	}
	h.messages = messages
	h.refs = make([]common.MessageRef, len(messages))
	h.unpersisted = make([]int, len(messages))
	for i := range messages {
		h.unpersisted[i] = i
	}
}
