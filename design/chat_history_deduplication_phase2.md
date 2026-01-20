# Phase 2: Migrate Workflow Code to ChatHistory Interface

## Goal

Replace direct usage of `*[]llm.ChatMessage` with `ChatHistoryContainer` throughout workflow code. At the end of this phase, all workflow code uses the new interface, but still backed by `LegacyChatHistory` so behavior is unchanged.

## Tasks

### 1. Update Function Signatures

Update functions in `dev/` package that accept or return `*[]llm.ChatMessage` to use `*ChatHistoryContainer` instead.

Key files and functions to update (not exhaustive, investigation required):
- `dev/edit_code.go` - `editCodeSubflow`, related functions
- `dev/build_dev_plan.go` - `buildDevPlanIteration`, related functions
- `dev/manage_chat_history.go` - `ManageChatHistory`, `ManageChatHistoryV2Activity`
- `dev/dev_workflow_signals.go` - signal handlers that pass chat history
- `dev/code_context.go` - functions that modify chat history

### 2. Replace Direct Slice Operations

Replace direct slice operations with interface methods:
- `append(*chatHistory, msg)` → `chatHistory.History.Append(msg)`
- `len(*chatHistory)` → `chatHistory.History.Len()`
- `(*chatHistory)[i]` → `chatHistory.History.Get(i)`
- `for _, msg := range *chatHistory` → iterate using `Len()` and `Get()`, or `Messages()`

Note: Since `LegacyChatHistory` stores `[]llm2.Message` internally, callers that need `llm.ChatMessage` will need to convert. Add helper functions as needed.

### 3. Update Chat History Initialization Points

Find all places where chat history is created (typically `chatHistory := []llm.ChatMessage{}` or similar) and replace with:
```go
chatHistory := &ChatHistoryContainer{History: NewLegacyChatHistory()}
```

Key locations (not exhaustive):
- `dev/edit_code.go` - where `editCodeSubflow` initializes history
- `dev/build_dev_plan.go` - where plan iteration initializes history

### 4. Update ManageChatHistory Workflow Function

**File:** `dev/manage_chat_history.go`

Update `ManageChatHistory` to work with `ChatHistoryContainer`. The activity call to `ManageChatHistoryV2Activity` should continue to work since the container marshals as `[]llm.ChatMessage`.

### 5. Update Tests

Update existing tests that create or manipulate chat history directly:
- `dev/manage_chat_history_test.go`
- `dev/edit_code_test.go`
- Other test files that construct `[]llm.ChatMessage`