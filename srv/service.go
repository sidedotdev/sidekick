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
	domain.ProjectStorage
	domain.FlowStorage
	domain.SubflowStorage
	domain.FlowActionStorage
	domain.WorkspaceStorage
	domain.WorktreeStorage
	domain.RemoteDeviceStorage
	common.KeyValueStorage

	CheckConnection(ctx context.Context) error
}

type Streamer interface {
	domain.TaskStreamer
	domain.FlowActionStreamer
	domain.FlowEventStreamer
}
