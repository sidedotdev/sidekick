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

### 5. Implement Llm2Activities

**File:** `persisted_ai/llm2_activities.go` (new file)

Create `Llm2Activities` struct with a `Stream` method that mirrors `LlmActivities.ChatStream` but uses `llm2` types:

```go
type StreamOptions struct {
    llm2.Options
    WorkspaceId  string
    FlowId       string
    FlowActionId string
}

type Llm2Activities struct {
    Streamer srv.Streamer
}

func (la *Llm2Activities) Stream(ctx context.Context, options StreamOptions) (*llm2.MessageResponse, error)
```

Key differences from `LlmActivities.ChatStream`:
- Uses `llm2.Options` (with `llm2.Params` containing `[]llm2.Message`) instead of `llm.ToolChatOptions`
- Uses `llm2.Provider` interface (`AnthropicResponsesProvider`, `OpenAIResponsesProvider`) instead of `llm.ToolChatter`
- Streams `llm2.Event` instead of `llm.ChatMessageDelta`
- Returns `*llm2.MessageResponse` instead of `*llm.ChatMessageResponse`
- Provider selection based on `options.Params.ModelConfig.Provider`

The streaming goroutine should:
- Convert `llm2.Event` to appropriate `domain` flow events for the Streamer
- Record heartbeats via `activity.RecordHeartbeat`
- Call `Streamer.EndFlowEventStream` on completion

Add helper function to select provider:
```go
func getLlm2Provider(config common.ModelConfig) (llm2.Provider, error)
```

### 6. Create ChatStream Helper

**File:** `dev/chat_stream_helper.go` (new file)

```go
func ExecuteChatStream(ctx workflow.Context, options persisted_ai.ChatStreamOptions, chatHistory *Llm2ChatHistory) (*llm2.MessageResponse, error)
```

This helper:
1. Auto-translates `ChatStreamOptions` to `StreamOptions` (converting `llm.ToolChatOptions` fields to `llm2.Options`)
2. Calls `Llm2Activities.Stream` with the translated options
3. Persists non-hydrated messages from the response back to the chat history via `chatHistory.AppendNonHydrated()`
4. Returns the response

This abstraction allows existing code to continue using `ChatStreamOptions` while internally using the new `llm2` infrastructure.

### 7. Register Activities

**File:** `worker/worker.go`

Register activities with the worker:
- `ChatHistoryActivities` (injecting `Storage`)
- `Llm2Activities` (injecting `Streamer`)

```go
chatHistoryActivities := &common.ChatHistoryActivities{
    Storage: service,
}
llm2Activities := &persisted_ai.Llm2Activities{
    Streamer: service,
}
// ...
w.RegisterActivity(chatHistoryActivities)
w.RegisterActivity(llm2Activities)
```

### 8. Unit Tests

**File:** `common/chat_history_test.go`

Add tests for `Llm2ChatHistory`:
- Marshal produces refs-only JSON
- Unmarshal + Hydrate restores full content
- Persist stores content blocks with correct keys
- Round-trip through marshal/unmarshal/hydrate preserves content

**File:** `dev/chat_history_activities_test.go` (new file)

Test `ManageV3` activity with mock storage.

**File:** `persisted_ai/llm2_activities_test.go` (new file)

Test `Llm2Activities.Stream`:
- Provider selection for openai/anthropic
- Event streaming and flow event conversion
- Heartbeat recording in activity context
- Error handling for unknown providers