package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/segmentio/ksuid"
)

// BlockIdGenerator is a function that generates unique block IDs.
// For workflow code, this should be backed by a deterministic side effect.
// For non-workflow code, this can use ksuid.New().String() directly.
type BlockIdGenerator func() string

const blockIdPrefix = "block_"

// NewBlockId generates a new unique block ID with the standard prefix.
func NewBlockId() string {
	return blockIdPrefix + ksuid.New().String()
}

// NewKsuidGenerator returns a BlockIdGenerator that uses ksuid.New().
func NewKsuidGenerator() BlockIdGenerator {
	return func() string {
		return NewBlockId()
	}
}

// MetadataNamespacePersistence is the namespace key for block persistence metadata.
const MetadataNamespacePersistence = "persistence"

// MetadataNamespaceSidekick is the namespace key for sidekick-specific block metadata.
const MetadataNamespaceSidekick = "sidekick"

// BlockMetadata provides access to the persisted block key for a
// content block. Implementations live in persisted_ai (BasicBlockMetadata)
// or in other packages that need custom metadata.
type BlockMetadata interface {
	GetBlockKey() string
	SetBlockKey(key string)
}

// BasicBlockMetadata is the default BlockMetadata implementation.
type BasicBlockMetadata struct {
	BlockKey string `json:"blockKey,omitempty"`
}

func (m *BasicBlockMetadata) GetBlockKey() string    { return m.BlockKey }
func (m *BasicBlockMetadata) SetBlockKey(key string) { m.BlockKey = key }

// SidekickBlockMetadata holds sidekick-specific metadata for a content block.
type SidekickBlockMetadata struct {
	ContextType string `json:"contextType,omitempty"`
}

// getSidekickBlockMetadata resolves the sidekick metadata from a content block,
// handling both the typed SidekickBlockMetadata case and the raw map[string]any
// case that results from JSON round-tripping.
func getSidekickBlockMetadata(block llm2.ContentBlock) *SidekickBlockMetadata {
	if block.Metadata == nil {
		return nil
	}
	raw := block.Metadata[MetadataNamespaceSidekick]
	if raw == nil {
		return nil
	}
	if meta, ok := raw.(*SidekickBlockMetadata); ok {
		return meta
	}
	if meta, ok := raw.(SidekickBlockMetadata); ok {
		return &meta
	}
	if m, ok := raw.(map[string]interface{}); ok {
		ct, _ := m["contextType"].(string)
		return &SidekickBlockMetadata{ContextType: ct}
	}
	return nil
}

// GetContextType returns the context type from a content block's sidekick
// metadata, or "" if none is set.
func GetContextType(block llm2.ContentBlock) string {
	if meta := getSidekickBlockMetadata(block); meta != nil {
		return meta.ContextType
	}
	return ""
}

// SetContextType sets the context type in a content block's sidekick metadata,
// initializing the metadata map if needed.
func SetContextType(block *llm2.ContentBlock, contextType string) {
	if block.Metadata == nil {
		block.Metadata = make(map[string]any)
	}
	meta := getSidekickBlockMetadata(*block)
	if meta == nil {
		meta = &SidekickBlockMetadata{}
	}
	meta.ContextType = contextType
	block.Metadata[MetadataNamespaceSidekick] = meta
}

// getBlockMetadata resolves the persistence metadata from a content block,
// handling both the typed BlockMetadata case and the raw map[string]interface{}
// case that results from JSON round-tripping.
func getBlockMetadata(block llm2.ContentBlock) BlockMetadata {
	if block.Metadata == nil {
		return nil
	}
	raw := block.Metadata[MetadataNamespacePersistence]
	if raw == nil {
		return nil
	}
	if meta, ok := raw.(BlockMetadata); ok {
		return meta
	}
	if m, ok := raw.(map[string]interface{}); ok {
		key, _ := m["blockKey"].(string)
		return &BasicBlockMetadata{BlockKey: key}
	}
	return nil
}

// GetBlockKey returns the persisted block key from a content block's
// persistence metadata, or "" if none is set.
func GetBlockKey(block llm2.ContentBlock) string {
	if meta := getBlockMetadata(block); meta != nil {
		return meta.GetBlockKey()
	}
	return ""
}

// SetBlockKey sets the persisted block key in a content block's
// persistence metadata, initializing the metadata map if needed.
func SetBlockKey(block *llm2.ContentBlock, key string) {
	if block.Metadata == nil {
		block.Metadata = make(map[string]any)
	}
	meta := getBlockMetadata(*block)
	if meta == nil {
		meta = &BasicBlockMetadata{}
	}
	meta.SetBlockKey(key)
	block.Metadata[MetadataNamespacePersistence] = meta
}

// clearBlockKeys removes persistence block keys from all content blocks in a
// message, so that Persist will re-store them.
func clearBlockKeys(msg *llm2.Message) {
	for i := range msg.Content {
		if GetBlockKey(msg.Content[i]) != "" {
			SetBlockKey(&msg.Content[i], "")
		}
	}
}

// MessageRef stores a reference to a message's content blocks in KV storage.
type MessageRef struct {
	BlockKeys []string `json:"blockKeys"`
	Role      string   `json:"role"`
}

// UnmarshalJSON supports both "blockKeys" and the legacy "blockIds" field.
// TODO(temporary): remove "blockIds" fallback once all persisted data uses "blockKeys".
func (r *MessageRef) UnmarshalJSON(data []byte) error {
	type alias MessageRef
	var raw struct {
		alias
		LegacyBlockIds []string `json:"blockIds"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = MessageRef(raw.alias)
	if len(r.BlockKeys) == 0 && len(raw.LegacyBlockIds) > 0 {
		r.BlockKeys = raw.LegacyBlockIds
	}
	return nil
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
	Clone() ChatHistory
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
	} else {
		x := MessageFromCommon(msg)
		role := common.ChatMessageRole(msg.GetRole())
		if len(x.Content[0].ToolResult.Name) > 0 {
			role = common.ChatMessageRoleTool
		}
		legacyMsg := common.ChatMessage{
			Role:      role,
			Content:   x.GetContentString(),
			ToolCalls: x.GetToolCalls(),

			Name:         x.Content[0].ToolResult.Name,
			ToolCallId:   x.Content[0].ToolResult.ToolCallId,
			IsError:      x.Content[0].ToolResult.IsError,
			CacheControl: x.Content[0].CacheControl,
			ContextType:  GetContextType(x.Content[0]),
		}
		h.messages = append(h.messages, legacyMsg)
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

func (h *LegacyChatHistory) Clone() ChatHistory {
	msgs := make([]common.ChatMessage, len(h.messages))
	copy(msgs, h.messages)
	return &LegacyChatHistory{messages: msgs}
}

// Llm2ChatHistory stores message references in Temporal history and persists
// actual content to KV storage for deduplication.
type Llm2ChatHistory struct {
	flowId         string
	workspaceId    string
	refs           []MessageRef
	messages       []llm2.Message
	hydrated       bool
	unpersisted    []int
	hydratedBlocks map[string]llm2.ContentBlock
}

// NewLlm2ChatHistory creates an empty, hydrated Llm2ChatHistory instance.
func NewLlm2ChatHistory(flowId, workspaceId string) *Llm2ChatHistory {
	return &Llm2ChatHistory{
		flowId:      flowId,
		workspaceId: workspaceId,
		refs:        []MessageRef{},
		messages:    []llm2.Message{},
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
		h.messages = []llm2.Message{}
	} else {
		h.hydrated = false
		h.messages = nil
	}
	return h
}

func (h *Llm2ChatHistory) Append(msg common.Message) {
	if !h.hydrated {
		panic("cannot append to non-hydrated Llm2ChatHistory; use AppendRef or activity-backed append")
	}
	var m llm2.Message
	if mp, ok := msg.(*llm2.Message); ok {
		m = *mp
	} else if cm, ok := msg.(common.ChatMessage); ok {
		m = MessageFromChatMessage(cm)
	} else if cmp, ok := msg.(*common.ChatMessage); ok {
		m = MessageFromChatMessage(*cmp)
	} else {
		return
	}

	h.messages = append(h.messages, m)
	h.unpersisted = append(h.unpersisted, len(h.messages)-1)
}

// MessageFromCommon converts a common.Message to an llm2.Message.
// Returns a zero Message if the type is not recognized.
func MessageFromCommon(msg common.Message) llm2.Message {
	if mp, ok := msg.(*llm2.Message); ok {
		return *mp
	}
	if cm, ok := msg.(common.ChatMessage); ok {
		return MessageFromChatMessage(cm)
	}
	if cmp, ok := msg.(*common.ChatMessage); ok {
		return MessageFromChatMessage(*cmp)
	}
	return llm2.Message{}
}

func MessageFromChatMessage(cm common.ChatMessage) llm2.Message {
	// Tool results in legacy format have role "tool", but in llm2 they should
	// be user-role messages with a ToolResult content block
	if cm.Role == common.ChatMessageRoleTool {
		block := llm2.ContentBlock{
			Type: llm2.ContentBlockTypeToolResult,
			ToolResult: &llm2.ToolResultBlock{
				ToolCallId: cm.ToolCallId,
				Name:       cm.Name,
				IsError:    cm.IsError,
				Content:    []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: cm.Content}},
			},
			CacheControl: cm.CacheControl,
		}
		if cm.ContextType != "" {
			SetContextType(&block, cm.ContextType)
		}
		return llm2.Message{
			Role:    llm2.RoleUser,
			Content: []llm2.ContentBlock{block},
		}
	}
	block := llm2.ContentBlock{
		Type:         llm2.ContentBlockTypeText,
		Text:         cm.Content,
		CacheControl: cm.CacheControl,
	}
	if cm.ContextType != "" {
		SetContextType(&block, cm.ContextType)
	}
	return llm2.Message{
		Role:    llm2.Role(cm.Role),
		Content: []llm2.ContentBlock{block},
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
	if mp, ok := msg.(*llm2.Message); ok {
		h.messages[index] = *mp
		clearBlockKeys(&h.messages[index])
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
func (h *Llm2ChatHistory) Llm2Messages() []llm2.Message {
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
		h.hydratedBlocks = make(map[string]llm2.ContentBlock)
	}

	// Populate cache from existing in-memory messages+refs before overwriting
	if h.messages != nil && len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			if i < len(h.messages) {
				msg := h.messages[i]
				for j, blockKey := range ref.BlockKeys {
					if j < len(msg.Content) {
						h.hydratedBlocks[blockKey] = msg.Content[j]
					}
				}
			}
		}
	}

	// Collect missing block keys and their storage keys
	var missingBlockKeys []string
	var missingStorageKeys []string
	for _, ref := range h.refs {
		for _, blockKey := range ref.BlockKeys {
			if _, ok := h.hydratedBlocks[blockKey]; !ok {
				missingBlockKeys = append(missingBlockKeys, blockKey)
				missingStorageKeys = append(missingStorageKeys, StorageKey(h.flowId, blockKey))
			}
		}
	}

	if len(missingBlockKeys) == 0 && len(h.refs) == 0 {
		h.messages = []llm2.Message{}
		h.hydrated = true
		return nil
	}

	// Fetch only missing content blocks using prefixed storage keys
	if len(missingStorageKeys) > 0 {
		values, err := storage.MGet(ctx, h.workspaceId, missingStorageKeys)
		if err != nil {
			return fmt.Errorf("failed to fetch content blocks: %w", err)
		}

		for i, blockKey := range missingBlockKeys {
			if i >= len(values) || values[i] == nil {
				continue
			}
			var block llm2.ContentBlock
			if err := json.Unmarshal(values[i], &block); err != nil {
				return fmt.Errorf("failed to unmarshal content block %s: %w", blockKey, err)
			}
			h.hydratedBlocks[blockKey] = block
		}
	}

	// Reconstruct messages from refs using the cache
	h.messages = make([]llm2.Message, len(h.refs))
	for i, ref := range h.refs {
		h.messages[i] = llm2.Message{
			Role:    llm2.Role(ref.Role),
			Content: make([]llm2.ContentBlock, 0, len(ref.BlockKeys)),
		}
		for _, blockKey := range ref.BlockKeys {
			if block, ok := h.hydratedBlocks[blockKey]; ok {
				SetBlockKey(&block, blockKey)
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
		h.hydratedBlocks = make(map[string]llm2.ContentBlock)
	}

	// Ensure refs slice is large enough
	for len(h.refs) < len(h.messages) {
		h.refs = append(h.refs, MessageRef{})
	}

	// Collect all content blocks to persist
	storageValues := make(map[string][]byte)
	for _, idx := range h.unpersisted {
		if idx < 0 || idx >= len(h.messages) {
			continue
		}
		msg := &h.messages[idx]
		blockKeys := make([]string, len(msg.Content))
		for j := range msg.Content {
			block := &msg.Content[j]
			if existingKey := GetBlockKey(*block); existingKey != "" {
				blockKeys[j] = existingKey
				continue
			}
			blockKey := gen()
			blockKeys[j] = blockKey
			SetBlockKey(block, blockKey)
			blockBytes, err := json.Marshal(*block)
			if err != nil {
				return fmt.Errorf("failed to marshal content block: %w", err)
			}
			storageValues[StorageKey(h.flowId, blockKey)] = blockBytes
			h.hydratedBlocks[blockKey] = *block
		}
		h.refs[idx] = MessageRef{
			BlockKeys: blockKeys,
			Role:      string(msg.Role),
		}
	}

	if len(storageValues) > 0 {
		if err := storage.MSetRaw(ctx, h.workspaceId, storageValues); err != nil {
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

func (h *Llm2ChatHistory) Clone() ChatHistory {
	refs := make([]MessageRef, len(h.refs))
	for i, ref := range h.refs {
		refs[i] = MessageRef{
			BlockKeys: append([]string(nil), ref.BlockKeys...),
			Role:      ref.Role,
		}
	}

	clone := &Llm2ChatHistory{
		flowId:      h.flowId,
		workspaceId: h.workspaceId,
		refs:        refs,
		hydrated:    h.hydrated,
	}

	if h.messages != nil {
		clone.messages = deepCopyMessages(h.messages)
	}

	clone.unpersisted = make([]int, len(h.unpersisted))
	copy(clone.unpersisted, h.unpersisted)

	if h.hydratedBlocks != nil {
		clone.hydratedBlocks = make(map[string]llm2.ContentBlock, len(h.hydratedBlocks))
		for k, v := range h.hydratedBlocks {
			clone.hydratedBlocks[k] = v
		}
	}

	return clone
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
		h.messages = []llm2.Message{}
	} else {
		h.hydrated = false
		h.messages = nil
	}
	return nil
}

// AppendRef adds a message reference without requiring hydration.
func (h *Llm2ChatHistory) AppendRef(ref MessageRef) {
	h.refs = append(h.refs, ref)
}

// FlowId returns the flow ID for the chat history.
func (h *Llm2ChatHistory) FlowId() string {
	return h.flowId
}

// SetFlowId sets the flow ID for the chat history.
func (h *Llm2ChatHistory) SetFlowId(flowId string) {
	h.flowId = flowId
}

// WorkspaceId returns the workspace ID for the chat history.
func (h *Llm2ChatHistory) WorkspaceId() string {
	return h.workspaceId
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
			BlockKeys: append([]string(nil), ref.BlockKeys...),
			Role:      ref.Role,
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
		h.hydratedBlocks = make(map[string]llm2.ContentBlock)
	}
	if h.hydrated && h.messages != nil && len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			if i < len(h.messages) {
				msg := h.messages[i]
				for j, blockKey := range ref.BlockKeys {
					if j < len(msg.Content) {
						h.hydratedBlocks[blockKey] = msg.Content[j]
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
// All messages are marked as unpersisted and block refs are cleared so that
// Persist will re-store the (potentially modified) content.
func (h *Llm2ChatHistory) SetMessages(messages []llm2.Message) {
	if !h.hydrated {
		panic("cannot set messages on non-hydrated Llm2ChatHistory")
	}
	h.messages = messages
	h.refs = make([]MessageRef, len(messages))
	h.unpersisted = make([]int, len(messages))
	for i := range messages {
		h.unpersisted[i] = i
		clearBlockKeys(&h.messages[i])
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
func (h *Llm2ChatHistory) SetHydratedWithMessages(messages []llm2.Message) {
	h.messages = messages
	h.hydrated = true

	// Populate the cache from the new messages and current refs
	if h.hydratedBlocks == nil {
		h.hydratedBlocks = make(map[string]llm2.ContentBlock)
	}
	if len(h.messages) == len(h.refs) {
		for i, ref := range h.refs {
			msg := h.messages[i]
			for j, blockKey := range ref.BlockKeys {
				if j < len(msg.Content) {
					h.hydratedBlocks[blockKey] = msg.Content[j]
				}
			}
		}
	}
}

// StorageKey returns the KV storage key for a block ID within a flow.
func StorageKey(flowId, blockId string) string {
	return fmt.Sprintf("%s:msg:%s", flowId, blockId)
}

// normalizeBlockId strips any "{flowId}:msg:" prefix from a block ID,
// returning just the bare identifier. This is idempotent for already-normalized IDs.
//
// TODO remove once all workflows started on the llm2_migration branch have
// completed or been terminated. Normalization is only needed to replay those
// workflows, which persisted flow-prefixed block IDs before we switched to
// bare IDs. After merging llm2_migration into main, new workflows will only
// produce bare IDs and this becomes a no-op.
func normalizeBlockId(blockId string) string {
	if idx := strings.LastIndex(blockId, ":msg:"); idx >= 0 {
		return blockId[idx+len(":msg:"):]
	}
	return blockId
}

// NormalizeBlockIds strips flow-prefixed block IDs down to bare identifiers.
// Call at workflow↔activity/side-effect boundaries to ensure deterministic replay
// regardless of how the block IDs were generated.
//
// TODO remove once all workflows started on the llm2_migration branch have
// completed or been terminated. See normalizeBlockId.
func (h *Llm2ChatHistory) NormalizeBlockIds() {
	for i, ref := range h.refs {
		for j, blockKey := range ref.BlockKeys {
			h.refs[i].BlockKeys[j] = normalizeBlockId(blockKey)
		}
	}
	// Re-key the cache under normalized IDs
	if h.hydratedBlocks != nil {
		normalized := make(map[string]llm2.ContentBlock, len(h.hydratedBlocks))
		for k, v := range h.hydratedBlocks {
			normalized[normalizeBlockId(k)] = v
		}
		h.hydratedBlocks = normalized
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

// Clone returns a deep copy of the container with a cloned underlying history.
func (c *ChatHistoryContainer) Clone() ChatHistoryContainer {
	if c.History == nil {
		return ChatHistoryContainer{}
	}
	return ChatHistoryContainer{History: c.History.Clone()}
}

// Llm2Messages returns the underlying []llm2.Message if the history is an Llm2ChatHistory,
// otherwise returns nil.
func (c *ChatHistoryContainer) Llm2Messages() []llm2.Message {
	if c == nil || c.History == nil {
		return nil
	}
	if llm2History, ok := c.History.(*Llm2ChatHistory); ok {
		return llm2History.Llm2Messages()
	}
	return nil
}

// NormalizeBlockIds normalizes block IDs on the underlying Llm2ChatHistory, if present.
//
// TODO remove once all workflows started on the llm2_migration branch have
// completed or been terminated. See normalizeBlockId.
func (c *ChatHistoryContainer) NormalizeBlockIds() {
	if c == nil || c.History == nil {
		return
	}
	if llm2History, ok := c.History.(*Llm2ChatHistory); ok {
		llm2History.NormalizeBlockIds()
	}
}
