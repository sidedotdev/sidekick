package poll_failures

import (
	"context"
	"errors"
	"fmt"
	"sidekick/db"
	"sidekick/models"
	"strings"

	workflowApi "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type PollFailuresActivities struct {
	TemporalClient   client.Client
	DatabaseAccessor db.DatabaseAccessor
}

type ListFailedWorkflowsInput struct {
	WorkspaceId string
}

func (a *PollFailuresActivities) ListFailedWorkflows(ctx context.Context, input ListFailedWorkflowsInput) ([]*workflowApi.WorkflowExecutionInfo, error) {
	request := workflowservice.ListWorkflowExecutionsRequest{
		Query: fmt.Sprintf("ExecutionStatus in ('Failed', 'Terminated', 'TimedOut') and WorkspaceId = '%s'", input.WorkspaceId),
	}
	resp, err := a.TemporalClient.ListWorkflow(ctx, &request)
	if err != nil {
		return nil, err
	}
	return resp.Executions, nil
}

type UpdateTaskStatusInput struct {
	WorkspaceId string
	FlowId      string
}

func (a *PollFailuresActivities) UpdateTaskStatus(ctx context.Context, input UpdateTaskStatusInput) error {
	flow, err := a.DatabaseAccessor.GetWorkflow(ctx, input.WorkspaceId, input.FlowId)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil
		} else {
			return err
		}
	}

	// parent isn't a task
	if !strings.HasPrefix(flow.ParentId, "task_") {
		return nil
	}

	task, err := a.DatabaseAccessor.GetTask(ctx, input.WorkspaceId, flow.ParentId)
	if err != nil {
		return err
	}

	if task.Status == models.TaskStatusFailed {
		return nil // idempotency
	}

	task.Status = models.TaskStatusFailed
	return a.DatabaseAccessor.PersistTask(ctx, task)
}
