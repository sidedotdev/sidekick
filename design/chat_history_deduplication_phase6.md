# Phase 6: Flow Content Cleanup

## Goal

Add cleanup mechanism to delete stored content blocks when a flow completes, preventing unbounded storage growth.

## Tasks

### 1. Add Cleanup Method to ChatHistoryActivities

**File:** `dev/chat_history_activities.go`

Add method to delete all content blocks for a flow:
```go
func (cha *ChatHistoryActivities) CleanupFlowContent(ctx context.Context, workspaceId, flowId string) error
```

May need to add a `DeleteByPrefix` method to `KeyValueStorage` interface.

### 2. Call Cleanup on Flow Completion

**File:** `dev/dev_agent_manager_workflow.go`

At flow completion (success or failure), execute cleanup activity. Cleanup failures should be logged but not fail the workflow.

### 3. Consider TTL Alternative

If the KV storage supports TTL, consider setting TTL on content blocks instead of explicit cleanup. This provides automatic cleanup even if the workflow crashes.