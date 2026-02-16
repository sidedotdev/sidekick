package common

import (
	"context"

	"go.temporal.io/sdk/workflow"
)

// WorkflowSafeKVStorage implements KeyValueStorage by executing
// KVActivities through the Temporal workflow context. This allows workflows
// to access KV storage while maintaining determinism.
// The caller should configure activity options on Ctx before use.
type WorkflowSafeKVStorage struct {
	Ctx workflow.Context
}

func (ws *WorkflowSafeKVStorage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	var ka *KVActivities
	var result [][]byte
	err := workflow.ExecuteActivity(ws.Ctx, ka.MGet, workspaceId, keys).Get(ws.Ctx, &result)
	return result, err
}

func (ws *WorkflowSafeKVStorage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	var ka *KVActivities
	err := workflow.ExecuteActivity(ws.Ctx, ka.MSet, workspaceId, values).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	var ka *KVActivities
	err := workflow.ExecuteActivity(ws.Ctx, ka.MSetRaw, workspaceId, values).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) DeletePrefix(ctx context.Context, workspaceId string, prefix string) error {
	var ka *KVActivities
	err := workflow.ExecuteActivity(ws.Ctx, ka.DeletePrefix, workspaceId, prefix).Get(ws.Ctx, nil)
	return err
}

func (ws *WorkflowSafeKVStorage) GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error) {
	var ka *KVActivities
	var result []string
	err := workflow.ExecuteActivity(ws.Ctx, ka.GetKeysWithPrefix, workspaceId, prefix).Get(ws.Ctx, &result)
	return result, err
}
