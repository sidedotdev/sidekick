package persisted_ai

import (
	"context"

	"sidekick/common"
)

// KVActivities provides Temporal activities for key-value storage operations.
// This allows workflows to access KV storage through activity calls.
type KVActivities struct {
	Storage common.KeyValueStorage
}

func (ka *KVActivities) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	return ka.Storage.MGet(ctx, workspaceId, keys)
}

func (ka *KVActivities) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	return ka.Storage.MSet(ctx, workspaceId, values)
}

func (ka *KVActivities) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	return ka.Storage.MSetRaw(ctx, workspaceId, values)
}

func (ka *KVActivities) DeletePrefix(ctx context.Context, workspaceId string, prefix string) error {
	return ka.Storage.DeletePrefix(ctx, workspaceId, prefix)
}

func (ka *KVActivities) GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error) {
	return ka.Storage.GetKeysWithPrefix(ctx, workspaceId, prefix)
}
