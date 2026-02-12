# Phase 4: Enable via Workflow Versioning

## Goal

Enable `Llm2ChatHistory` for new workflows via Temporal versioning, while existing workflows continue using `LegacyChatHistory`. This is where the space savings are realized.

## Tasks

### 1. Add Version Checks at Initialization Points

At each point where chat history is initialized, add version check:

```go
v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)
var chatHistory *ChatHistoryContainer
if v == 1 {
    chatHistory = &ChatHistoryContainer{History: NewLlm2ChatHistory(flowId, workspaceId)}
} else {
    chatHistory = &ChatHistoryContainer{History: NewLegacyChatHistory()}
}
```

Key locations (not exhaustive, investigation required):
- `dev/edit_code.go` - `editCodeSubflow`
- `dev/build_dev_plan.go` - `buildDevPlanIteration`

### 2. Update ManageChatHistory to Use ManageV3

**File:** `dev/manage_chat_history.go`

Update `ManageChatHistory` workflow function to call `cha.ManageV3` for versioned workflows:

```go
v := workflow.GetVersion(ctx, "manage-chat-history-v3", workflow.DefaultVersion, 1)
if v == 1 {
    var cha *ChatHistoryActivities
    workflow.ExecuteActivity(ctx, cha.ManageV3, chatHistory, maxLength)
} else {
    // existing ManageChatHistoryV2Activity call
}
```

### 3. Update ChatStream Callers

Replace direct ChatStream calls with the helper from Phase 3:

Before:
```go
var la *persisted_ai.LlmActivities
err := flow_action.PerformWithUserRetry(ctx, la.ChatStream, &response, options)
```

After:
```go
response, err := ExecuteChatStreamWithHistory(actionCtx, chatHistory, options)
```

Key locations (not exhaustive):
- `dev/edit_code.go`
- `dev/build_dev_plan.go`
- Other files calling `ChatStream` with chat history

### 4. Use ChatStream Helper

Replace direct `LlmActivities.ChatStream` calls with the `ExecuteChatStream` helper from Phase 3:

Before:
```go
var la *persisted_ai.LlmActivities
options := persisted_ai.ChatStreamOptions{
    ToolChatOptions: llm.ToolChatOptions{...},
    WorkspaceId:     workspaceId,
    FlowId:          flowId,
    FlowActionId:    flowActionId,
}
var response llm.ChatMessageResponse
err := workflow.ExecuteActivity(ctx, la.ChatStream, options).Get(ctx, &response)
// manually append response to chat history
```

After:
```go
options := persisted_ai.ChatStreamOptions{
    ToolChatOptions: llm.ToolChatOptions{...},
    WorkspaceId:     workspaceId,
    FlowId:          flowId,
    FlowActionId:    flowActionId,
}
response, err := dev.ExecuteChatStream(ctx, options, chatHistory)
// response messages are automatically persisted to chatHistory as non-hydrated
```

The helper internally:
1. Translates `ChatStreamOptions` to `StreamOptions` (mapping `llm.ToolChatOptions` fields to `llm2.Options`)
2. Hydrates the chat history if needed
3. Calls `Llm2Activities.Stream`
4. Persists the response messages as non-hydrated entries in the chat history

This approach:
- Minimizes changes at call sites (same `ChatStreamOptions` struct)
- Centralizes the translation logic in one place
- Automatically handles response persistence
- Is gated by the same workflow version check used for `Llm2ChatHistory` initialization

### 5. Ensure Hydration Before Content Access

Any workflow code that accesses message content must ensure hydration first. For activities, call `Hydrate()` at the start. The `ManageV3` activity handles this internally.

The `ExecuteChatStream` helper handles hydration automatically before building the `llm2.Options.Params.Messages`.