package dev

import (
	"context"
	"sidekick/domain"
	"sidekick/mocks"
	"sidekick/srv/redis"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newDevAgentManagerActivities() *DevAgentManagerActivities {
	return &DevAgentManagerActivities{
		Storage:        redis.NewTestRedisStorage(),
		TemporalClient: &mocks.Client{},
	}
}

func TestUpdateTaskForUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities()
	storage := redis.NewTestRedisStorage()

	workspaceId := "testWorkspace"
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_testTask",
	}
	flow := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "workflow_testWorkflow",
		ParentId:    task.Id,
	}
	err := storage.PersistTask(context.Background(), task)
	assert.Nil(t, err)

	err = storage.PersistFlow(context.Background(), flow)
	assert.Nil(t, err)

	err = ima.UpdateTaskForUserRequest(context.Background(), workspaceId, flow.Id)
	assert.Nil(t, err)

	// Retrieve the task from the database
	updatedTask, err := storage.GetTask(context.Background(), workspaceId, task.Id)
	assert.Nil(t, err)

	// Check that the task was updated appropriately
	assert.Equal(t, domain.AgentTypeHuman, updatedTask.AgentType)
	assert.Equal(t, domain.TaskStatusBlocked, updatedTask.Status)
}

func TestCreatePendingUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities()
	storage := redis.NewTestRedisStorage()
	ctx := context.Background()

	workspaceId := "testWorkspace"
	flowId := "fakeWorkflowId"
	request := RequestForUser{
		OriginWorkflowId: flowId,
		Content:          "request content",
		Subflow:          "fakeSubflow",
		RequestKind:      RequestKindFreeForm,
	}

	var flowAction domain.FlowAction
	err := ima.CreatePendingUserRequest(ctx, workspaceId, request)
	assert.Nil(t, err)

	flowActions, err := storage.GetFlowActions(context.Background(), workspaceId, flowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)

	flowAction = flowActions[0]

	assert.Equal(t, "user_request", flowAction.ActionType)
	assert.Equal(t, map[string]interface{}{
		"requestContent": request.Content,
		"requestKind":    string(request.RequestKind),
	}, flowAction.ActionParams)
	assert.Equal(t, domain.ActionStatusPending, flowAction.ActionStatus)

	// Retrieve the flow action from the database
	persitedFlowAction, err := storage.GetFlowAction(context.Background(), workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the flow action was persisted appropriately
	assert.Equal(t, flowAction, persitedFlowAction)
}

func TestExistingUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities()
	storage := redis.NewTestRedisStorage()
	ctx := context.Background()

	workspaceId := "testWorkspace"
	flowId := "fakeWorkflowId"
	flowActionId := "fakeFlowActionId"
	request := RequestForUser{
		OriginWorkflowId: flowId,
		FlowActionId:     flowActionId,
		Content:          "request content",
		Subflow:          "fakeSubflow",
		RequestKind:      RequestKindApproval,
	}

	existingFlowAction := domain.FlowAction{
		Id:          flowActionId,
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		ActionType:  "another_action",
		ActionParams: map[string]interface{}{
			"requestContent": request.Content,
			"requestKind":    string(request.RequestKind),
		},
		ActionStatus: domain.ActionStatusStarted,
	}
	err := storage.PersistFlowAction(ctx, existingFlowAction)
	assert.Nil(t, err)

	var flowAction domain.FlowAction
	err = ima.CreatePendingUserRequest(ctx, workspaceId, request)
	assert.Nil(t, err)

	flowActions, err := storage.GetFlowActions(context.Background(), workspaceId, flowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)

	flowAction = flowActions[0]
	assert.Equal(t, flowAction, existingFlowAction)
	assert.Equal(t, utils.PanicJSON(flowAction), utils.PanicJSON(existingFlowAction))
}
