# Phase 6: Flow Content Cleanup

## Goal

Add cleanup mechanism to delete stored content blocks when a flow is deleted (i.e. the parent task is deleted), preventing unbounded storage growth.

## Tasks

### 1. Add Cleanup Method to ChatHistoryActivities

**File:** `dev/chat_history_activities.go`

Add method to delete all content blocks for a flow:
```go
func (cha *ChatHistoryActivities) CleanupFlowContent(ctx context.Context, workspaceId, flowId string) error
```

May need to add a `DeleteByPrefix` method to `KeyValueStorage` interface.

### 2. Call Cleanup on Task Deletion

...