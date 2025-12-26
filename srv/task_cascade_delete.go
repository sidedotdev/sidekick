package srv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
)

type CascadeDeleteTaskInput struct {
	WorkspaceId string
	TaskId      string
}

type CascadeDeleteTaskActivities struct {
	Service        Service
	TemporalClient client.Client
}

type taskSnapshot struct {
	Task        domain.Task
	Flows       []domain.Flow
	FlowActions map[string][]domain.FlowAction // flowId -> actions
}

func CascadeDeleteTaskWorkflow(ctx workflow.Context, input CascadeDeleteTaskInput) error {
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var activities *CascadeDeleteTaskActivities

	// Step 1: Build snapshot of all data to be deleted (for compensation)
	var snapshot taskSnapshot
	err := workflow.ExecuteActivity(ctx, activities.BuildSnapshot, input.WorkspaceId, input.TaskId).Get(ctx, &snapshot)
	if err != nil {
		logger.Error("Failed to build snapshot", "error", err)
		return err
	}

	// Step 2: Terminate all flow workflows (best-effort)
	for _, flow := range snapshot.Flows {
		var terminated bool
		err := workflow.ExecuteActivity(ctx, activities.TerminateFlowWorkflow, flow.Id).Get(ctx, &terminated)
		if err != nil {
			logger.Warn("Failed to terminate flow workflow", "flowId", flow.Id, "error", err)
			// Continue anyway - workflow may already be gone
		}
	}

	// Step 3: Delete flow actions for each flow
	for _, flow := range snapshot.Flows {
		err := workflow.ExecuteActivity(ctx, activities.DeleteFlowActions, input.WorkspaceId, flow.Id).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete flow actions, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot)
			return err
		}
	}

	// Step 4: Delete flows
	for _, flow := range snapshot.Flows {
		err := workflow.ExecuteActivity(ctx, activities.DeleteFlow, input.WorkspaceId, flow.Id).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete flow, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot)
			return err
		}
	}

	// Step 5: Delete task
	err = workflow.ExecuteActivity(ctx, activities.DeleteTask, input.WorkspaceId, input.TaskId).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to delete task, compensating", "error", err)
		compensate(ctx, activities, snapshot)
		return err
	}

	// Step 6: Delete KV prefix for llm2 message blocks (last, as specified)
	for _, flow := range snapshot.Flows {
		prefix := flow.Id + ":msg:"
		err := workflow.ExecuteActivity(ctx, activities.DeleteKVPrefix, input.WorkspaceId, prefix).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete KV prefix, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot)
			return err
		}
	}

	return nil
}

func compensate(ctx workflow.Context, activities *CascadeDeleteTaskActivities, snapshot taskSnapshot) {
	logger := workflow.GetLogger(ctx)

	// Use a disconnected context for compensation to ensure it runs even if workflow is cancelled
	compensateCtx, _ := workflow.NewDisconnectedContext(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
	}
	compensateCtx = workflow.WithActivityOptions(compensateCtx, ao)

	// Restore task
	err := workflow.ExecuteActivity(compensateCtx, activities.RestoreTask, snapshot.Task).Get(compensateCtx, nil)
	if err != nil {
		logger.Error("Failed to restore task during compensation", "error", err)
	}

	// Restore flows
	for _, flow := range snapshot.Flows {
		err := workflow.ExecuteActivity(compensateCtx, activities.RestoreFlow, flow).Get(compensateCtx, nil)
		if err != nil {
			logger.Error("Failed to restore flow during compensation", "flowId", flow.Id, "error", err)
		}
	}

	// Restore flow actions
	for flowId, actions := range snapshot.FlowActions {
		for _, action := range actions {
			err := workflow.ExecuteActivity(compensateCtx, activities.RestoreFlowAction, action).Get(compensateCtx, nil)
			if err != nil {
				logger.Error("Failed to restore flow action during compensation", "flowId", flowId, "actionId", action.Id, "error", err)
			}
		}
	}
}

// BuildSnapshot loads task, flows, and flow actions into a snapshot for compensation
func (a *CascadeDeleteTaskActivities) BuildSnapshot(ctx context.Context, workspaceId, taskId string) (taskSnapshot, error) {
	task, err := a.Service.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return taskSnapshot{}, fmt.Errorf("failed to get task: %w", err)
	}

	flows, err := a.Service.GetFlowsForTask(ctx, workspaceId, taskId)
	if err != nil {
		return taskSnapshot{}, fmt.Errorf("failed to get flows: %w", err)
	}

	flowActions := make(map[string][]domain.FlowAction)
	for _, flow := range flows {
		actions, err := a.Service.GetFlowActions(ctx, workspaceId, flow.Id)
		if err != nil {
			return taskSnapshot{}, fmt.Errorf("failed to get flow actions for flow %s: %w", flow.Id, err)
		}
		flowActions[flow.Id] = actions
	}

	return taskSnapshot{
		Task:        task,
		Flows:       flows,
		FlowActions: flowActions,
	}, nil
}

const temporalLiteNotFoundError1 = "sql: no rows"
const temporalLiteAlreadyCompletedError = "workflow execution already completed"
const temporalWorkflowNotFoundForId = "workflow not found for ID"

// TerminateFlowWorkflow terminates a flow's Temporal workflow (best-effort)
func (a *CascadeDeleteTaskActivities) TerminateFlowWorkflow(ctx context.Context, flowId string) (bool, error) {
	reason := "CascadeDeleteTask cleanup"
	err := a.TemporalClient.TerminateWorkflow(ctx, flowId, "", reason)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, temporalWorkflowNotFoundForId) ||
			strings.Contains(errStr, temporalLiteNotFoundError1) ||
			strings.Contains(errStr, temporalLiteAlreadyCompletedError) {
			log.Debug().Str("flowId", flowId).Msg("Workflow already terminated or not found")
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteFlowActions deletes all flow actions for a flow
func (a *CascadeDeleteTaskActivities) DeleteFlowActions(ctx context.Context, workspaceId, flowId string) error {
	return a.Service.DeleteFlowActionsForFlow(ctx, workspaceId, flowId)
}

// DeleteFlow deletes a flow
func (a *CascadeDeleteTaskActivities) DeleteFlow(ctx context.Context, workspaceId, flowId string) error {
	return a.Service.DeleteFlow(ctx, workspaceId, flowId)
}

// DeleteTask deletes a task
func (a *CascadeDeleteTaskActivities) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
	return a.Service.DeleteTask(ctx, workspaceId, taskId)
}

// DeleteKVPrefix deletes all KV entries with the given prefix
func (a *CascadeDeleteTaskActivities) DeleteKVPrefix(ctx context.Context, workspaceId, prefix string) error {
	return a.Service.DeletePrefix(ctx, workspaceId, prefix)
}

// RestoreTask re-persists a task (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreTask(ctx context.Context, task domain.Task) error {
	return a.Service.PersistTask(ctx, task)
}

// RestoreFlow re-persists a flow (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreFlow(ctx context.Context, flow domain.Flow) error {
	return a.Service.PersistFlow(ctx, flow)
}

// RestoreFlowAction re-persists a flow action (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreFlowAction(ctx context.Context, action domain.FlowAction) error {
	return a.Service.PersistFlowAction(ctx, action)
}
