# Chat History Deduplication Design

## Problem Statement

Temporal event histories are space-inefficient, with a 100MB history showing:

| Activity | Calls | Total Size (bytes) | % of History |
|----------|-------|-------------------|--------------|
| PersistFlowAction | 1,832 | 39,200,208 | 36.7% |
| ManageChatHistoryV2Activity | 453 | 37,983,918 | 35.6% |
| ChatStream | 453 | 19,031,271 | 17.8% |

The root cause: chat history is passed as full `[]ChatMessage` across activity boundaries, and this history is highly duplicative.

## Design Goals

1. Reduce Temporal event history size by 80%+ for chat-history-heavy workflows
2. Maintain backward compatibility with existing workflows
3. Combine with llm2 migration to avoid duplicate work
4. Minimize workflow versioning footprint

## Proposed Solution: ChatHistory Interface with Container

### Core Concept

Introduce a `ChatHistory` interface with two implementations:
- **LegacyChatHistory**: Wraps `[]llm.ChatMessage`, marshals identically (backward compatible)
- **Llm2ChatHistory**: Wraps `[]llm2.Message`, marshals only references to content stored in KV database

A `ChatHistoryContainer` wraps the interface for JSON marshaling/unmarshaling, with smart detection of legacy `[]llm.ChatMessage` format for full backward compatibility.

### Message Interface

To abstract over `llm.ChatMessage` and `llm2.Message`, introduce a minimal `Message` interface that both can implement. This allows the `ChatHistory` interface to avoid referencing either concrete type directly:

```go
// Message abstracts over llm.ChatMessage and llm2.Message.
// This interface will be removed in a future version (e.g., v0.8 or v0.9)
// when we clean up old workflow version code.
type Message interface {
    GetRole() string
    GetContentString() string
    GetContentBlocks() []llm2.ContentBlock
}
```

Both `llm.ChatMessage` and `llm2.Message` can implement this interface:
- `llm.ChatMessage`: `GetContentBlocks()` converts the single `Content` string to a single text block
- `llm2.Message`: `GetContentString()` concatenates text from all text blocks

### Storage Interface

Add to `srv` package and embed in `Storage` interface:

```go
type KeyValueStorage interface {
    MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)
    MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error
}
```

### ChatHistory Interface (Minimal)

Keep the interface minimal, using the `Message` interface to avoid coupling to either `llm.ChatMessage` or `llm2.Message` directly:

```go
type ChatHistory interface {
    // Core operations
    Append(msg Message)
    Len() int
    Get(index int) Message
    Messages() []Message
    
    // Hydration: loads content from storage, returns error if storage call fails
    // No-op for LegacyChatHistory. For Llm2ChatHistory, fetches content blocks via MGet.
    Hydrate(ctx context.Context, storage KeyValueStorage) error
    
    // Persist: stores any unpersisted content blocks, returns error if storage call fails
    // No-op for LegacyChatHistory. For Llm2ChatHistory, stores via MSet.
    Persist(ctx context.Context, storage KeyValueStorage) error
}
```

### ChatHistoryContainer

```go
type ChatHistoryContainer struct {
    History ChatHistory
}

func (c *ChatHistoryContainer) UnmarshalJSON(data []byte) error {
    // Smart detection: try to unmarshal as []llm.ChatMessage first
    // If that works, convert to llm2.Message slice and wrap in LegacyChatHistory
    // Otherwise, unmarshal as Llm2ChatHistory refs
}

func (c *ChatHistoryContainer) MarshalJSON() ([]byte, error) {
    // Delegates to underlying History
    // LegacyChatHistory marshals as []llm.ChatMessage (for backward compat)
    // Llm2ChatHistory marshals as refs only
}
```

### Llm2ChatHistory

```go
type Llm2ChatHistory struct {
    flowId      string
    workspaceId string
    refs        []MessageRef      // persisted references
    messages    []llm2.Message    // hydrated content
    hydrated    bool
    unpersisted []int             // indices of messages not yet persisted
}
```

### Helper for ChatStream Execution

Instead of calling ChatStream directly, use a helper that handles persistence:

```go
func ExecuteChatStreamWithHistory(actionCtx ActionContext, chatHistory *ChatHistoryContainer, options ChatStreamOptions) (*llm.ChatMessageResponse, error) {
    // 1. Persist any unpersisted blocks (no-op for legacy)
    // 2. Execute ChatStream via PerformWithUserRetry
    // 3. Return response
}
```

### ChatHistoryActivities Struct

```go
type ChatHistoryActivities struct {
    Storage srv.KeyValueStorage
}

func (cha *ChatHistoryActivities) ManageV3(ctx context.Context, history ChatHistoryContainer, maxLength int) (ChatHistoryContainer, error) {
    // Hydrate, run manage logic, persist, return
}
```

### Storage Key Design

```
chat_block:{workspace_id}:{flow_id}:{block_id}
```

Where:
- `workspace_id` is the workspace the flow belongs to
- `flow_id` is the flow ID (enables cleanup of messages for old flows)
- `block_id` is the `Id` field on `llm2.ContentBlock`. If the `Id` field is empty, generate a new ksuid before persisting.

---

# Project Plan

See individual phase files for detailed instructions:
- `design/chat_history_deduplication_phase1.md` - Interface Foundation
- `design/chat_history_deduplication_phase2.md` - Migrate Workflow Code
- `design/chat_history_deduplication_phase3.md` - Implement Llm2ChatHistory
- `design/chat_history_deduplication_phase4.md` - Enable via Workflow Versioning
- `design/chat_history_deduplication_phase5.md` - Frontend Support for New Format
- `design/chat_history_deduplication_phase6.md` - Flow Content Cleanup