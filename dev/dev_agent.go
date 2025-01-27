package dev

import (
	"context"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/llm"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

type DevAgent struct {
	TemporalClient    client.Client
	TemporalTaskQueue string
	WorkspaceId       string
	ChatHistory       *[]llm.ChatMessage
}

func (ia *DevAgent) getRunID(ctx context.Context, workflowID string) string {
	handle := ia.TemporalClient.GetWorkflow(ctx, workflowID, "")
	return handle.GetRunID()
}

func (ia *DevAgent) workRequest(ctx context.Context, parentId, request, flowType string, flowOptions map[string]interface{}) (domain.Flow, error) {
	devManagerWorkflowId, err := ia.findOrStartDevAgentManagerWorkflow(ctx, ia.WorkspaceId)
	if err != nil {
		return domain.Flow{}, fmt.Errorf("error finding or starting dev manager workflow: %w", err)
	}

	workRequest := WorkRequest{ParentId: parentId, Input: request, FlowType: flowType, FlowOptions: flowOptions}
	//updateHandle, err := ia.TemporalClient.UpdateWorkflow(ctx, devManagerWorkflowId, "", UpdateNameWorkRequest, workRequest)
	updateRequest := client.UpdateWorkflowOptions{
		UpdateID:   uuid.New().String(),
		WorkflowID: devManagerWorkflowId,
		RunID:      ia.getRunID(ctx, devManagerWorkflowId),
		UpdateName: UpdateNameWorkRequest,
		Args:       []interface{}{workRequest},

		// How this RPC should block on the server before returning.
		WaitForStage: client.WorkflowUpdateStageAccepted,
	}
	updateHandle, err := ia.TemporalClient.UpdateWorkflow(ctx, updateRequest)
	if err != nil {
		return domain.Flow{}, fmt.Errorf("error issuing Update request: %w\n%v", err, updateRequest)
	}

	var flow domain.Flow
	err = updateHandle.Get(ctx, &flow)
	if err != nil {
		return domain.Flow{}, fmt.Errorf("update encountered an error: %w", err)
	}
	return flow, nil
}

func (ia *DevAgent) RelayResponse(ctx context.Context, userResponse UserResponse) error {
	log.Printf("relaying response to workflow: %s\n", userResponse.TargetWorkflowId)
	devManagerWorkflowId, err := ia.findOrStartDevAgentManagerWorkflow(ctx, ia.WorkspaceId)
	if err != nil {
		return fmt.Errorf("error finding or starting dev manager workflow: %w", err)
	}

	err = ia.TemporalClient.SignalWorkflow(ctx, devManagerWorkflowId, "", SignalNameUserResponse, userResponse)
	return err
}

func (ia DevAgent) findOrStartDevAgentManagerWorkflow(ctx context.Context, workspaceId string) (string, error) {
	workflowId := workspaceId + "_dev_manager"
	workflowRetryPolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        100 * time.Second,
		MaximumAttempts:        1000,                         // up to 1000 retries
		NonRetryableErrorTypes: []string{"OutOfBoundsError"}, // Out-of-bounds errors are non-retryable
	}
	options := client.StartWorkflowOptions{
		ID:                    workflowId,
		TaskQueue:             ia.TemporalTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy:           workflowRetryPolicy,
		SearchAttributes:      map[string]interface{}{"WorkspaceId": workspaceId},
	}

	we, err := ia.TemporalClient.ExecuteWorkflow(ctx, options, DevAgentManagerWorkflow, DevAgentManagerWorkflowInput{
		WorkspaceId: workspaceId,
	})
	if err != nil {
		// fmt.Printf("Failed to start dev manager workflow: %v\n", err)
		return "", err
	}
	// fmt.Printf("Started dev manager workflow: %s\n", we.GetID())
	return we.GetID(), nil
}

func (ia DevAgent) HandleNewTask(ctx context.Context, task *domain.Task) error {
	// perform a work request where the parentId is the taskId and the task description is the request
	_, err := ia.workRequest(ctx, task.Id, task.Description, task.FlowType, task.FlowOptions)
	if err != nil {
		return err
	}
	return nil
}

const temporalLiteNotFoundError1 = "no rows in result set"
const temporalLiteAlreadyCompletedError = "workflow execution already completed"
const temporalWorkflowNotFoundForId = "workflow not found for ID"

// TerminateWorkflowIfExists terminates a workflow execution if there is one running
func (ia *DevAgent) TerminateWorkflowIfExists(ctx context.Context, workflowId string) error {
	reason := "DevAgent TerminateWorkflowIfExists"
	err := ia.TemporalClient.TerminateWorkflow(ctx, workflowId, "", reason)
	if err != nil && !strings.Contains(err.Error(), temporalWorkflowNotFoundForId) && !strings.Contains(err.Error(), temporalLiteNotFoundError1) && !strings.Contains(err.Error(), temporalLiteAlreadyCompletedError) {
		fmt.Printf("failed to terminate workflow: %v\n", err)
		return fmt.Errorf("failed to terminate workflow: %w", err)
	}
	return nil
}
