package db

import (
	"context"
	"sidekick/domain"
)

// TODO split this into multiple interfaces: TaskService, TaskStreamService, FlowService, FlowStreamService etc. Leave CheckConnection, MGet and MSet in this interface.
// DatabaseAccessor interface is a composition of all the other interfaces
type DatabaseAccessor interface {
	domain.TaskService
	domain.TaskStreamService
	domain.FlowService
	domain.FlowStreamService
	domain.WorkspaceService

	CheckConnection(ctx context.Context) error

	// TODO add workspaceId to this
	MGet(ctx context.Context, keys []string) ([]interface{}, error)
	// TODO add workspaceId to this
	MSet(ctx context.Context, values map[string]interface{}) error
}
