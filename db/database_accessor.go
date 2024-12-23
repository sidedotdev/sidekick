package db

import (
	"context"
	"sidekick/domain"
	"time"
)

// TODO split this into multiple interfaces: TaskService, TaskStreamService, FlowService, FlowStreamService etc. Leave CheckConnection, MGet and MSet in this interface.
// then we'll retain the DatabaseAccessor interface as a composition of all the other interfaces
type DatabaseAccessor interface {
	CheckConnection(ctx context.Context) error
	PersistWorkflow(ctx context.Context, flow domain.Flow) error
	GetWorkflow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error)
	GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error)
	PersistSubflow(ctx context.Context, subflow domain.Subflow) error
	GetSubflows(ctx context.Context, workspaceId, flowId string) ([]domain.Subflow, error)
	PersistTask(ctx context.Context, task domain.Task) error
	GetTask(ctx context.Context, workspaceId, taskId string) (domain.Task, error)
	GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error)
	PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error
	GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]domain.FlowAction, error)
	GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.FlowAction, string, error)
	GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (domain.FlowAction, error)
	GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.Task, string, error)
	DeleteTask(ctx context.Context, workspaceId, taskId string) error
	PersistWorkspace(ctx context.Context, workspace domain.Workspace) error
	GetWorkspace(ctx context.Context, workspaceId string) (domain.Workspace, error)
	GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error)
	GetWorkspaceConfig(ctx context.Context, workspaceId string) (domain.WorkspaceConfig, error)
	PersistWorkspaceConfig(ctx context.Context, workspaceId string, config domain.WorkspaceConfig) error

	// TODO add workspaceId to this
	MGet(ctx context.Context, keys []string) ([]interface{}, error)
	// TODO add workspaceId to this
	MSet(ctx context.Context, values map[string]interface{}) error
}
