package db

import (
	"context"
	"sidekick/models"
	"time"
)

// TODO split this into multiple interfaces, TopicAccessor, MessageAccessor, TaskAcessor, KVAccessor etc.
type DatabaseAccessor interface {
	PersistTopic(ctx context.Context, topic models.Topic) error
	GetTopics(ctx context.Context, workspaceId string) ([]models.Topic, error)
	PersistMessage(ctx context.Context, message models.Message) error
	PersistWorkflow(ctx context.Context, flow models.Flow) error
	GetWorkflow(ctx context.Context, workspaceId, flowId string) (models.Flow, error)
	GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]models.Flow, error)
	PersistSubflow(ctx context.Context, subflow models.Subflow) error
	GetSubflows(ctx context.Context, workspaceId, flowId string) ([]models.Subflow, error)
	GetMessages(ctx context.Context, workspaceId, topicId string) ([]models.Message, error)
	TopicExists(ctx context.Context, workspaceId, topicId string) (bool, error)
	PersistTask(ctx context.Context, task models.Task) error
	GetTask(ctx context.Context, workspaceId, taskId string) (models.Task, error)
	GetTasks(ctx context.Context, workspaceId string, statuses []models.TaskStatus) ([]models.Task, error)
	PersistFlowAction(ctx context.Context, flowAction models.FlowAction) error
	GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]models.FlowAction, error)
	GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]models.FlowAction, string, error)
	GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (models.FlowAction, error)
	GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]models.Task, string, error)
	DeleteTask(ctx context.Context, workspaceId, taskId string) error
	PersistWorkspace(ctx context.Context, workspace models.Workspace) error
	GetWorkspace(ctx context.Context, workspaceId string) (models.Workspace, error)
	GetAllWorkspaces(ctx context.Context) ([]models.Workspace, error)
	GetWorkspaceConfig(ctx context.Context, workspaceId string) (models.WorkspaceConfig, error)
	PersistWorkspaceConfig(ctx context.Context, workspaceId string, config models.WorkspaceConfig) error

	// TODO add workspaceId to this
	MGet(ctx context.Context, keys []string) ([]interface{}, error)
	// TODO add workspaceId to this
	MSet(ctx context.Context, values map[string]interface{}) error
}
