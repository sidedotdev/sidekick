package db

import (
	"context"
	"sidekick/domain"
)

type Service interface {
	StorageService
	StreamService
}

type StorageService interface {
	domain.TaskService
	domain.FlowService
	domain.WorkspaceService

	CheckConnection(ctx context.Context) error

	// TODO add workspaceId to this
	MGet(ctx context.Context, keys []string) ([]interface{}, error)
	// TODO add workspaceId to this
	MSet(ctx context.Context, values map[string]interface{}) error
}

type StreamService interface {
	domain.TaskStreamService
	domain.FlowStreamService
}