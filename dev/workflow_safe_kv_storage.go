package dev

import (
	"context"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"sidekick/persisted_ai"
)

// WorkflowSafeKVStorage implements common.KeyValueStorage by executing
// KVActivities through the Temporal workflow context. This allows workflows
// to access KV storage while maintaining determinism.
type WorkflowSafeKVStorage struct {
	Ctx         workflow.Context
	WorkspaceId string
}

func (ws *WorkflowSafeKVStorage) activityCtx() workflow.Context {
	return workflow.WithActivityOptions(ws.Ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    100 * time.Millisecond,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	})
}

func (ws *WorkflowSafeKVStorage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	var ka *persisted_ai.KVActivities
	var result [][]byte
	err := workflow.ExecuteActivity(ws.activityCtx(), ka.MGet, workspaceId, keys).Get(ws.Ctx, &result)
	return result, err
}

func (ws *WorkflowSafeKVStorage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	var ka *persisted_ai.KVActivities
	err := workflow.ExecuteActivity(ws.activityCtx(), ka.MSet, workspaceId, values).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	var ka *persisted_ai.KVActivities
	err := workflow.ExecuteActivity(ws.activityCtx(), ka.MSetRaw, workspaceId, values).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) DeletePrefix(ctx context.Context, workspaceId string, prefix string) error {
	var ka *persisted_ai.KVActivities
	err := workflow.ExecuteActivity(ws.activityCtx(), ka.DeletePrefix, workspaceId, prefix).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error) {
	var ka *persisted_ai.KVActivities
	var result []string
	err := workflow.ExecuteActivity(ws.activityCtx(), ka.GetKeysWithPrefix, workspaceId, prefix).Get(ws.Ctx, &result)
	return result, err
}
