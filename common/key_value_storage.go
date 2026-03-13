package common

import (
	"context"
)

// KeyValueStorage provides key-value storage operations.
// This is the canonical interface; srv.Storage embeds common.KeyValueStorage.
type KeyValueStorage interface {
	MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)
	MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error
	MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error
	// DeletePrefix deletes all keys matching the given prefix for a workspace.
	// This operation is NOT atomic or transactional: keys are deleted in batches,
	// so a failure partway through may leave some matching keys deleted and others
	// still present.
	DeletePrefix(ctx context.Context, workspaceId string, prefix string) error
	GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error)
}
