package dev

import (
	"context"
	"fmt"
	"sidekick/db"
	"sidekick/llm"
	"sidekick/models"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

type DevAgentManagerActivities struct {
	DatabaseAccessor db.DatabaseAccessor
	TemporalClient   client.Client
}

func (ima *DevAgentManagerActivities) UpdateTaskForUserRequest(ctx context.Context, workspaceId, workflowId string) error {
	// Recursive function to find a workflow record with parent_id that starts with "task_"
	var findWorkflowParentTaskId func(string) (string, error)
	findWorkflowParentTaskId = func(currentWorkflowId string) (string, error) {
		flow, err := ima.DatabaseAccessor.GetWorkflow(ctx, workspaceId, currentWorkflowId)
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
	task, err := ima.DatabaseAccessor.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return fmt.Errorf("Failed to retrieve task record for taskId %s: %v", taskId, err)
	}

	task.AgentType = models.AgentTypeHuman
	task.Status = models.TaskStatusBlocked
	task.Updated = time.Now()

	return ima.DatabaseAccessor.PersistTask(ctx, task)
}

func (ima *DevAgentManagerActivities) PutWorkflow(ctx context.Context, flow models.Flow) (err error) {
	err = ima.DatabaseAccessor.PersistWorkflow(ctx, flow)
	return err
}

func (ima *DevAgentManagerActivities) CompleteFlowParentTask(ctx context.Context, workspaceId, parentId, flowStatus string) (err error) {
	// Retrieve the task using workspaceId and parentId
	task, err := ima.DatabaseAccessor.GetTask(ctx, workspaceId, parentId)
	if err != nil {
		return err
	}

	var taskStatus models.TaskStatus
	if flowStatus == "completed" {
		taskStatus = models.TaskStatusComplete
	} else {
		taskStatus = models.TaskStatusFailed
	}
	task.Status = taskStatus
	task.AgentType = models.AgentTypeNone
	err = ima.DatabaseAccessor.PersistTask(ctx, task)
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

func (ima *DevAgentManagerActivities) GetWorkflow(ctx context.Context, workspaceId, workflowId string) (message models.Flow, err error) {
	log := activity.GetLogger(ctx)
	flow, err := ima.DatabaseAccessor.GetWorkflow(ctx, workspaceId, workflowId)
	if err != nil {
		log.Error("Failed to retrieve workflow record", "Error", err)
		return models.Flow{}, err
	}
	return flow, nil
}

func (ima *DevAgentManagerActivities) CreateRequestForUserMessageRecord(ctx context.Context, workspaceId string, req RequestForUser) (message models.Message, err error) {
	log := activity.GetLogger(ctx)
	flow, err := ima.DatabaseAccessor.GetWorkflow(ctx, workspaceId, req.OriginWorkflowId)
	if err != nil {
		log.Error("Failed to retrieve workflow record", "Error", err)
		return message, err
	}

	allMessages, err := ima.DatabaseAccessor.GetMessages(ctx, workspaceId, flow.TopicId)
	if err != nil {
		log.Error("Failed to retrieve existing message records", "Error", err)
		return message, err
	}
	existing := false
	for _, msg := range allMessages {
		if msg.FlowId == req.OriginWorkflowId && msg.Content == req.Content {
			existing = true
			message = msg
			break
		}
	}
	if existing {
		// A message with the same content already exists, return without creating a new message
		log.Info("There is already a message with the same content, exiting early to remain idempotent")
		return message, err
	}

	message = models.Message{
		WorkspaceId: workspaceId,
		TopicId:     flow.TopicId,
		Id:          "message_" + ksuid.New().String(),
		Role:        string(llm.ChatMessageRoleAssistant),
		Content:     req.Content,
		Created:     time.Now().UTC(),
		FlowId:      req.OriginWorkflowId,
	}
	err = ima.DatabaseAccessor.PersistMessage(ctx, message)
	if err != nil {
		log.Error("Failed to create message record", "Error", err)
		return message, err
	}

	return message, nil
}

func (ima *DevAgentManagerActivities) CreatePendingUserRequest(ctx context.Context, workspaceId string, req RequestForUser) error {
	if req.FlowActionId == "" {
		flowAction := models.FlowAction{
			WorkspaceId:      workspaceId,
			Id:               "fa_" + ksuid.New().String(),
			FlowId:           req.OriginWorkflowId,
			Created:          time.Now().UTC(),
			Updated:          time.Now().UTC(),
			SubflowName:      req.Subflow,
			ActionType:       "user_request",
			ActionParams:     req.ActionParams(),
			ActionStatus:     models.ActionStatusPending,
			IsHumanAction:    true,
			IsCallbackAction: true,
		}

		err := ima.DatabaseAccessor.PersistFlowAction(ctx, flowAction)
		if err != nil {
			return fmt.Errorf("Failed to persist flow action: %v", err)
		}
	} else {
		_, err := ima.DatabaseAccessor.GetFlowAction(ctx, workspaceId, req.FlowActionId)
		if err != nil {
			if err == db.ErrNotFound {
				return fmt.Errorf("Flow action with id %s not found in workspace %s", req.FlowActionId, workspaceId)
			}
			return fmt.Errorf("Failed to find existing flow action: %v", err)
		}
	}

	return nil
}

func (ima *DevAgentManagerActivities) FindWorkspaceById(ctx context.Context, workspaceId string) (models.Workspace, error) {
	log := activity.GetLogger(ctx)
	workspace, err := ima.DatabaseAccessor.GetWorkspace(ctx, workspaceId)
	if err != nil {
		log.Error("Failed to retrieve workspace record", "Error", err)
		return models.Workspace{}, err
	}
	return workspace, nil
}
