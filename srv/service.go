package srv

import (
	"context"
	"sidekick/domain"
)

type Service interface {
	Storage
	Streamer
	DeleteWorkspace(ctx context.Context, workspaceId string) error
}

type Storage interface {
	domain.TaskStorage
	domain.FlowStorage
	domain.SubflowStorage
	domain.FlowActionStorage
	domain.WorkspaceStorage

	CheckConnection(ctx context.Context) error

	MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)
	MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error

	DeleteWorkspace(ctx context.Context, workspaceId string) error
}

type Streamer interface {
	domain.TaskStreamer
	domain.FlowActionStreamer
	domain.FlowEventStreamer
}
