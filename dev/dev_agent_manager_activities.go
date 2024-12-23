package dev

import (
	"context"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

type DevAgentManagerActivities struct {
	Storage        srv.Storage
	TemporalClient client.Client
}

func (ima *DevAgentManagerActivities) UpdateTaskForUserRequest(ctx context.Context, workspaceId, workflowId string) error {
	// Recursive function to find a workflow record with parent_id that starts with "task_"
	var findWorkflowParentTaskId func(string) (string, error)
	findWorkflowParentTaskId = func(currentWorkflowId string) (string, error) {
		flow, err := ima.Storage.GetFlow(ctx, workspaceId, currentWorkflowId)
		if err != nil {
			return "", fmt.Errorf("Failed to retrieve workflow record for workflowId %s: %v", currentWorkflowId, err)
		}

		if strings.HasPrefix(flow.ParentId, "task_") {
			return flow.ParentId, nil
		} else if strings.HasPrefix(flow.ParentId, "workflow_") {
			return findWorkflowParentTaskId(flow.ParentId)
		}

		return "", fmt.Errorf("No task workflow found for workflowId: %s", workflowId)
	}

	// Find the task id
	taskId, err := findWorkflowParentTaskId(workflowId)
	if err != nil {
		return err
	}

	// Update the task record
	task, err := ima.Storage.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return fmt.Errorf("Failed to retrieve task record for taskId %s: %v", taskId, err)
	}

	task.AgentType = domain.AgentTypeHuman
	task.Status = domain.TaskStatusBlocked
	task.Updated = time.Now()

	return ima.Storage.PersistTask(ctx, task)
}

func (ima *DevAgentManagerActivities) PutWorkflow(ctx context.Context, flow domain.Flow) (err error) {
	err = ima.Storage.PersistFlow(ctx, flow)
	return err
}

func (ima *DevAgentManagerActivities) CompleteFlowParentTask(ctx context.Context, workspaceId, parentId, flowStatus string) (err error) {
	// Retrieve the task using workspaceId and parentId
	task, err := ima.Storage.GetTask(ctx, workspaceId, parentId)
	if err != nil {
		return err
	}

	var taskStatus domain.TaskStatus
	if flowStatus == "completed" {
		taskStatus = domain.TaskStatusComplete
	} else {
		taskStatus = domain.TaskStatusFailed
	}
	task.Status = taskStatus
	task.AgentType = domain.AgentTypeNone
	err = ima.Storage.PersistTask(ctx, task)
	if err != nil {
		return err
	}

	return nil
}

func (ima *DevAgentManagerActivities) PassOnUserResponse(userResponse UserResponse) (err error) {
	err = ima.TemporalClient.SignalWorkflow(context.Background(), userResponse.TargetWorkflowId, "", SignalNameUserResponse, userResponse)
	if err != nil && err.Error() == "workflow execution already completed" {
		log.Warn().Msg("we tried to pass on a user response to a workflow that already completed, something must be wrong")
		return nil
	}
	return err
}

func (ima *DevAgentManagerActivities) GetWorkflow(ctx context.Context, workspaceId, workflowId string) (message domain.Flow, err error) {
	log := activity.GetLogger(ctx)
	flow, err := ima.Storage.GetFlow(ctx, workspaceId, workflowId)
	if err != nil {
		log.Error("Failed to retrieve workflow record", "Error", err)
		return domain.Flow{}, err
	}
	return flow, nil
}

func (ima *DevAgentManagerActivities) CreatePendingUserRequest(ctx context.Context, workspaceId string, req RequestForUser) error {
	if req.FlowActionId == "" {
		flowAction := domain.FlowAction{
			WorkspaceId:      workspaceId,
			Id:               "fa_" + ksuid.New().String(),
			FlowId:           req.OriginWorkflowId,
			Created:          time.Now().UTC(),
			Updated:          time.Now().UTC(),
			SubflowName:      req.Subflow,
			ActionType:       "user_request",
			ActionParams:     req.ActionParams(),
			ActionStatus:     domain.ActionStatusPending,
			IsHumanAction:    true,
			IsCallbackAction: true,
		}

		err := ima.Storage.PersistFlowAction(ctx, flowAction)
		if err != nil {
			return fmt.Errorf("Failed to persist flow action: %v", err)
		}
	} else {
		_, err := ima.Storage.GetFlowAction(ctx, workspaceId, req.FlowActionId)
		if err != nil {
			if err == srv.ErrNotFound {
				return fmt.Errorf("Flow action with id %s not found in workspace %s", req.FlowActionId, workspaceId)
			}
			return fmt.Errorf("Failed to find existing flow action: %v", err)
		}
	}

	return nil
}

func (ima *DevAgentManagerActivities) FindWorkspaceById(ctx context.Context, workspaceId string) (domain.Workspace, error) {
	log := activity.GetLogger(ctx)
	workspace, err := ima.Storage.GetWorkspace(ctx, workspaceId)
	if err != nil {
		log.Error("Failed to retrieve workspace record", "Error", err)
		return domain.Workspace{}, err
	}
	return workspace, nil
}
