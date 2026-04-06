package dev

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"strings"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
)

type DevAgent struct {
	TemporalClient    client.Client
	TemporalTaskQueue string
	WorkspaceId       string
}

func (ia *DevAgent) RelayResponse(ctx context.Context, userResponse flow_action.UserResponse) error {
	log.Info().Str("workflowId", userResponse.TargetWorkflowId).Msg("relaying response to workflow")
	err := ia.TemporalClient.SignalWorkflow(ctx, userResponse.TargetWorkflowId, "", flow_action.SignalNameUserResponse, userResponse)
	if err != nil && strings.Contains(err.Error(), temporalLiteAlreadyCompletedError) {
		log.Warn().Msg("tried to relay user response to a workflow that already completed")
		return nil
	}
	return err
}

// TaskWorkflowId returns the deterministic Temporal workflow ID for a task's
// orchestrator workflow, allowing callers to cancel or query it by task ID.
func TaskWorkflowId(taskId string) string {
	return "task_wf_" + taskId
}

func (ia DevAgent) HandleNewTask(ctx context.Context, task *domain.Task) error {
	options := client.StartWorkflowOptions{
		ID:        TaskWorkflowId(task.Id),
		TaskQueue: ia.TemporalTaskQueue,
		Memo: map[string]interface{}{
			"sidekickVersion": common.GetBuildCommitSha(),
		},
	}
	_, err := ia.TemporalClient.ExecuteWorkflow(ctx, options, TaskWorkflow, TaskWorkflowInput{
		WorkspaceId: task.WorkspaceId,
		TaskId:      task.Id,
		FlowType:    task.FlowType,
		FlowOptions: task.FlowOptions,
		Description: task.Description,
	})
	return err
}

const temporalLiteNotFoundError1 = "no rows in result set"
const temporalLiteAlreadyCompletedError = "workflow execution already completed"
const temporalWorkflowNotFoundForId = "workflow not found for ID"

// TerminateWorkflowIfExists terminates a workflow execution if there is one running
func (ia *DevAgent) TerminateWorkflowIfExists(ctx context.Context, workflowId string) error {
	reason := "DevAgent TerminateWorkflowIfExists"
	err := ia.TemporalClient.TerminateWorkflow(ctx, workflowId, "", reason)
	if err != nil && !strings.Contains(err.Error(), temporalWorkflowNotFoundForId) && !strings.Contains(err.Error(), temporalLiteNotFoundError1) && !strings.Contains(err.Error(), temporalLiteAlreadyCompletedError) {
		log.Error().Err(err).Msg("failed to terminate workflow")
		return fmt.Errorf("failed to terminate workflow: %w", err)
	}
	return nil
}
