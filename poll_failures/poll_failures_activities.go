package poll_failures

import (
	"context"
	"errors"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"
	"strings"

	workflowApi "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type PollFailuresActivities struct {
	TemporalClient client.Client
	Service        srv.Service
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
	flow, err := a.Service.GetFlow(ctx, input.WorkspaceId, input.FlowId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return nil
		} else {
			return err
		}
	}

	// parent isn't a task
	if !strings.HasPrefix(flow.ParentId, "task_") {
		return nil
	}

	task, err := a.Service.GetTask(ctx, input.WorkspaceId, flow.ParentId)
	if err != nil {
		return err
	}

	if task.Status == domain.TaskStatusFailed {
		return nil // idempotency
	}

	task.Status = domain.TaskStatusFailed
	return a.Service.PersistTask(ctx, task)
}
