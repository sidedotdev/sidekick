package srv

import (
	"context"
	"errors"
	"fmt"
	"sidekick/domain"
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

func (d *Delegator) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	return d.streamer.StreamTaskChanges(ctx, workspaceId, streamMessageStartId)
}

func (d *Delegator) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	return d.streamer.StreamFlowActionChanges(ctx, workspaceId, flowId, streamMessageStartId)
}

func (d Delegator) PersistWorktree(ctx context.Context, worktree domain.Worktree) error {
	return d.storage.PersistWorktree(ctx, worktree)
}

func (d Delegator) GetWorktree(ctx context.Context, workspaceId, worktreeId string) (domain.Worktree, error) {
	return d.storage.GetWorktree(ctx, workspaceId, worktreeId)
}

func (d Delegator) GetWorktrees(ctx context.Context, workspaceId string) ([]domain.Worktree, error) {
	return d.storage.GetWorktrees(ctx, workspaceId)
}

func (d Delegator) GetWorktreesForFlow(ctx context.Context, workspaceId string, flowId string) ([]domain.Worktree, error) {
	return d.storage.GetWorktreesForFlow(ctx, workspaceId, flowId)
}

func (d Delegator) DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error {
	return d.storage.DeleteWorktree(ctx, workspaceId, worktreeId)
}

/* implements Storage interface */
func (d Delegator) CheckConnection(ctx context.Context) error {
	return d.storage.CheckConnection(ctx)
}

/* implements Storage interface */
func (d Delegator) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	return d.storage.MGet(ctx, workspaceId, keys)
}

/* implements Storage interface */
func (d Delegator) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	return d.storage.MSet(ctx, workspaceId, values)
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

func (d *Delegator) DeleteWorkspace(ctx context.Context, workspaceId string) error {
	return d.storage.DeleteWorkspace(ctx, workspaceId)
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

/* implements TaskStorage interface */
func (d Delegator) GetArchivedTasks(ctx context.Context, workspaceId string, offset, limit int64) ([]domain.Task, int64, error) {
	return d.storage.GetArchivedTasks(ctx, workspaceId, offset, limit)
}

/* implements TaskStreamer interface */
func (d Delegator) AddTaskChange(ctx context.Context, task domain.Task) error {
	return d.streamer.AddTaskChange(ctx, task)
}

/* implements FlowStorage interface */
func (d Delegator) PersistFlow(ctx context.Context, flow domain.Flow) error {
	// Attempt to get the existing flow
	existingFlow, err := d.storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("error getting existing flow: %w", err)
	}

	var statusChangeEvent *domain.StatusChangeEvent

	// Check if it's a new flow or if the status has changed
	if errors.Is(err, ErrNotFound) || existingFlow.Status != flow.Status {
		statusChangeEvent = &domain.StatusChangeEvent{
			EventType: domain.StatusChangeEventType,
			ParentId:  flow.Id,
			Status:    flow.Status,
		}
	}

	// Persist the new or updated flow
	if err := d.storage.PersistFlow(ctx, flow); err != nil {
		return fmt.Errorf("error persisting flow: %w", err)
	}

	// If we have a status change event, add it using the streamer
	if statusChangeEvent != nil {
		if err := d.streamer.AddFlowEvent(ctx, flow.WorkspaceId, flow.Id, statusChangeEvent); err != nil {
			return fmt.Errorf("error adding flow event: %w", err)
		}
	}

	return nil
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
	// Attempt to get the existing subflow
	existingSubflow, err := d.storage.GetSubflow(ctx, subflow.WorkspaceId, subflow.Id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("error getting existing subflow: %w", err)
	}

	var statusChangeEvent *domain.StatusChangeEvent

	// Check if it's a new subflow or if the status has changed
	if errors.Is(err, ErrNotFound) || existingSubflow.Status != subflow.Status {
		statusChangeEvent = &domain.StatusChangeEvent{
			EventType: domain.StatusChangeEventType,
			ParentId:  subflow.Id,
			Status:    string(subflow.Status),
		}
	}

	// Persist the new or updated subflow
	if err := d.storage.PersistSubflow(ctx, subflow); err != nil {
		return fmt.Errorf("error persisting subflow: %w", err)
	}

	// If we have a status change event, add it using the streamer
	if statusChangeEvent != nil {
		if err := d.streamer.AddFlowEvent(ctx, subflow.WorkspaceId, subflow.FlowId, statusChangeEvent); err != nil {
			return fmt.Errorf("error adding flow event: %w", err)
		}
	}

	return nil
}

/* implements FlowStorage interface */
func (d Delegator) GetSubflows(ctx context.Context, workspaceId string, flowId string) ([]domain.Subflow, error) {
	return d.storage.GetSubflows(ctx, workspaceId, flowId)
}

/* implements SubflowStorage interface */
func (d Delegator) GetSubflow(ctx context.Context, workspaceId string, subflowId string) (domain.Subflow, error) {
	return d.storage.GetSubflow(ctx, workspaceId, subflowId)
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

/* implements FlowEventStreamer interface */
func (d Delegator) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	return d.streamer.AddFlowEvent(ctx, workspaceId, flowId, flowEvent)
}

/* implements FlowEventStreamer interface */
func (d Delegator) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	return d.streamer.EndFlowEventStream(ctx, workspaceId, flowId, eventStreamParentId)
}

func (d Delegator) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	return d.streamer.StreamFlowEvents(ctx, workspaceId, flowId, subscriptionCh)
}

/* implements MCPEventStreamer interface */
func (d Delegator) AddMCPToolCallEvent(ctx context.Context, workspaceId, sessionId string, event domain.MCPToolCallEvent) error {
	return d.streamer.AddMCPToolCallEvent(ctx, workspaceId, sessionId, event)
}
