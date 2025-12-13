# Phase 3: Implement Llm2ChatHistory

## Goal

Implement the `Llm2ChatHistory` type that stores only references in Temporal history and persists actual content to the KV database. This phase adds the implementation but does not yet enable it in workflows.

## Tasks

### 1. Define MessageRef Type

**File:** `common/chat_history.go`

Define `MessageRef` struct for serialized references:
```go
type MessageRef struct {
    FlowId   string   `json:"flowId"`
    BlockIds []string `json:"blockIds"` // IDs of content blocks
    Role     string   `json:"role"`
}
```

### 2. Implement Llm2ChatHistory

**File:** `common/chat_history.go`

Implement `Llm2ChatHistory` struct:
- Fields: `flowId`, `workspaceId`, `refs []MessageRef`, `messages []llm2.Message`, `hydrated bool`, `unpersisted []int`
- `Append()`: adds message, marks index as unpersisted
- `Len()`, `Get()`, `Messages()`: standard accessors (panic or error if not hydrated when accessing content)
- `Hydrate()`: calls `storage.MGet()` to fetch content blocks by ID, populates `messages`
- `Persist()`: calls `storage.MSet()` for unpersisted messages, clears unpersisted list
- `MarshalJSON()`: outputs only `refs` array
- `UnmarshalJSON()`: populates `refs`, sets `hydrated = false`

Constructor: `NewLlm2ChatHistory(flowId, workspaceId string) *Llm2ChatHistory`

### 3. Update ChatHistoryContainer Unmarshaling

**File:** `common/chat_history.go`

Update `UnmarshalJSON` to detect format:
1. Try parsing as `[]llm.ChatMessage` (legacy format) → wrap in `LegacyChatHistory`
2. Try parsing as `[]MessageRef` (new format) → wrap in `Llm2ChatHistory`

### 4. Create ChatHistoryActivities Struct

**File:** `dev/chat_history_activities.go` (new file)

```go
type ChatHistoryActivities struct {
    Storage srv.KeyValueStorage
}

func (cha *ChatHistoryActivities) ManageV3(ctx context.Context, history ChatHistoryContainer, maxLength int) (ChatHistoryContainer, error) {
    // 1. Hydrate if needed
    // 2. Run existing ManageChatHistoryV2Activity logic on messages
    // 3. Persist
    // 4. Return updated container
}
```

### 5. Create ChatStream Helper

**File:** `dev/chat_stream_helper.go` (new file)

Create helper function that wraps ChatStream execution:
- Calls `Persist()` on chat history before executing (no-op for legacy)
- Executes ChatStream via `PerformWithUserRetry`
- Returns response

This helper will be used in Phase 4 to replace direct ChatStream calls.

### 6. Register Activities

**File:** `worker/worker.go`

Register `ChatHistoryActivities` with the worker, injecting `Storage`.

### 7. Unit Tests

**File:** `common/chat_history_test.go`

Add tests for `Llm2ChatHistory`:
- Marshal produces refs-only JSON
- Unmarshal + Hydrate restores full content
- Persist stores content blocks with correct keys
- Round-trip through marshal/unmarshal/hydrate preserves content

**File:** `dev/chat_history_activities_test.go` (new file)

Test `ManageV3` activity with mock storage.