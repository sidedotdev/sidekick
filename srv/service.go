package srv

import (
	"context"
	"sidekick/domain"
)

type Service interface {
	Storage
	Streamer
	DeleteWorkspace(ctx context.Context, workspaceId string) error
	DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error
}

type KeyValueStorage interface {
	MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)
	MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error
}

type Storage interface {
	domain.TaskStorage
	domain.FlowStorage
	domain.SubflowStorage
	domain.FlowActionStorage
	domain.WorkspaceStorage
	domain.WorktreeStorage
	KeyValueStorage

	CheckConnection(ctx context.Context) error
}

type Streamer interface {
	domain.TaskStreamer
	domain.FlowActionStreamer
	domain.FlowEventStreamer
}
