package srv

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
)

type Service interface {
	Storage
	Streamer
	DeleteWorkspace(ctx context.Context, workspaceId string) error
	DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error
}

type Storage interface {
	domain.TaskStorage
	domain.FlowStorage
	domain.SubflowStorage
	domain.FlowActionStorage
	domain.WorkspaceStorage
	domain.WorktreeStorage
	common.KeyValueStorage

	CheckConnection(ctx context.Context) error
}

type Streamer interface {
	domain.TaskStreamer
	domain.FlowActionStreamer
	domain.FlowEventStreamer
}
