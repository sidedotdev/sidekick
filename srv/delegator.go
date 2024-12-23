package srv

import (
	"context"
	"sidekick/domain"
	"time"
)

/* Delegates calls, but also decorates storage with streaming for events/change
 * tracking */
type Delegator struct {
	storage  Storage
	streamer Streamer
}

func NewDelegator(storage Storage, streamer Streamer) *Delegator {
	return &Delegator{
		storage:  storage,
		streamer: streamer,
	}
}

/* implements Storage interface */
func (d Delegator) CheckConnection(ctx context.Context) error {
	return d.storage.CheckConnection(ctx)
}

/* implements Storage interface */
func (d Delegator) MGet(ctx context.Context, keys []string) ([]interface{}, error) {
	return d.storage.MGet(ctx, keys)
}

/* implements Storage interface */
func (d Delegator) MSet(ctx context.Context, values map[string]interface{}) error {
	return d.storage.MSet(ctx, values)
}

/* implements WorkspaceStorage interface */
func (d Delegator) PersistWorkspace(ctx context.Context, workspace domain.Workspace) error {
	return d.storage.PersistWorkspace(ctx, workspace)
}

/* implements WorkspaceStorage interface */
func (d Delegator) GetWorkspace(ctx context.Context, workspaceId string) (domain.Workspace, error) {
	return d.storage.GetWorkspace(ctx, workspaceId)
}

/* implements WorkspaceStorage interface */
func (d Delegator) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	return d.storage.GetAllWorkspaces(ctx)
}

/* implements WorkspaceStorage interface */
func (d Delegator) GetWorkspaceConfig(ctx context.Context, workspaceId string) (domain.WorkspaceConfig, error) {
	return d.storage.GetWorkspaceConfig(ctx, workspaceId)
}

/* implements WorkspaceStorage interface */
func (d Delegator) PersistWorkspaceConfig(ctx context.Context, workspaceId string, config domain.WorkspaceConfig) error {
	return d.storage.PersistWorkspaceConfig(ctx, workspaceId, config)
}

/* implements TaskStorage interface */
func (d Delegator) GetTask(ctx context.Context, workspaceId string, taskId string) (domain.Task, error) {
	return d.storage.GetTask(ctx, workspaceId, taskId)
}

/* implements TaskStorage interface */
func (d Delegator) PersistTask(ctx context.Context, task domain.Task) error {
	err := d.storage.PersistTask(ctx, task)
	if err != nil {
		return err
	}
	return d.AddTaskChange(ctx, task)
}

/* implements TaskStorage interface */
func (d Delegator) DeleteTask(ctx context.Context, workspaceId string, taskId string) error {
	return d.storage.DeleteTask(ctx, workspaceId, taskId)
}

/* implements TaskStorage interface */
func (d Delegator) GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error) {
	return d.storage.GetTasks(ctx, workspaceId, statuses)
}

/* implements TaskStreamer interface */
func (d Delegator) AddTaskChange(ctx context.Context, task domain.Task) error {
	return d.streamer.AddTaskChange(ctx, task)
}

/* implements TaskStreamer interface */
func (d Delegator) GetTaskChanges(ctx context.Context, workspaceId string, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.Task, string, error) {
	return d.streamer.GetTaskChanges(ctx, workspaceId, streamMessageStartId, maxCount, blockDuration)
}

/* implements FlowStorage interface */
func (d Delegator) PersistFlow(ctx context.Context, workflow domain.Flow) error {
	err := d.storage.PersistFlow(ctx, workflow)
	return err
	// TODO
	/*
		if err != nil {
			return err
		}
		return d.Streamer.AddFlowChange(ctx, workflow)
	*/
}

/* implements FlowStorage interface */
func (d Delegator) GetFlow(ctx context.Context, workspaceId string, flowId string) (domain.Flow, error) {
	return d.storage.GetFlow(ctx, workspaceId, flowId)
}

/* implements FlowStorage interface */
func (d Delegator) GetFlowsForTask(ctx context.Context, workspaceId string, taskId string) ([]domain.Flow, error) {
	return d.storage.GetFlowsForTask(ctx, workspaceId, taskId)
}

/* implements FlowStorage interface */
func (d Delegator) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	return d.storage.PersistSubflow(ctx, subflow)
}

/* implements FlowStorage interface */
func (d Delegator) GetSubflows(ctx context.Context, workspaceId string, flowId string) ([]domain.Subflow, error) {
	return d.storage.GetSubflows(ctx, workspaceId, flowId)
}

/* implements FlowStorage interface */
func (d Delegator) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	err := d.storage.PersistFlowAction(ctx, flowAction)
	if err != nil {
		return err
	}
	return d.AddFlowActionChange(ctx, flowAction)
}

/* implements FlowStorage interface */
func (d Delegator) GetFlowActions(ctx context.Context, workspaceId string, flowId string) ([]domain.FlowAction, error) {
	return d.storage.GetFlowActions(ctx, workspaceId, flowId)
}

/* implements FlowStorage interface */
func (d Delegator) GetFlowAction(ctx context.Context, workspaceId string, flowActionId string) (domain.FlowAction, error) {
	return d.storage.GetFlowAction(ctx, workspaceId, flowActionId)
}

/* implements FlowStreamer interface */
func (d Delegator) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
	return d.streamer.AddFlowActionChange(ctx, flowAction)
}

/* implements FlowStreamer interface */
func (d Delegator) GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.FlowAction, string, error) {
	return d.streamer.GetFlowActionChanges(ctx, workspaceId, flowId, streamMessageStartId, maxCount, blockDuration)
}

/* implements FlowEventStreamer interface */
func (d Delegator) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	return d.streamer.AddFlowEvent(ctx, workspaceId, flowId, flowEvent)
}

/* implements FlowEventStreamer interface */
func (d Delegator) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	return d.streamer.EndFlowEventStream(ctx, workspaceId, flowId, eventStreamParentId)
}

/* implements FlowEventStreamer interface */
func (d Delegator) GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]domain.FlowEvent, map[string]string, error) {
	return d.streamer.GetFlowEvents(ctx, workspaceId, streamKeys, maxCount, blockDuration)
}
