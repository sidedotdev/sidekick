package redis

import (
	"context"
	"sidekick/domain"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestGetFlowActions(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisStorage()
	flowAction1 := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "test-flow-id",
		Id:          "test-id1z",
		// other fields...
	}
	flowAction2 := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "test-flow-id",
		Id:          "test-id1a",
		// other fields...
	}

	err := db.PersistFlowAction(ctx, flowAction1)
	assert.Nil(t, err)
	err = db.PersistFlowAction(ctx, flowAction2)
	assert.Nil(t, err)

	flowActions, err := db.GetFlowActions(ctx, flowAction1.WorkspaceId, flowAction1.FlowId)
	assert.Nil(t, err)
	assert.Contains(t, flowActions, flowAction1)
	assert.Contains(t, flowActions, flowAction2)

	// Check that the flow actions are retrieved in the order they were persisted
	assert.Equal(t, flowAction1, flowActions[0])
	assert.Equal(t, flowAction2, flowActions[1])
}

func TestPersistFlowAction(t *testing.T) {
	ctx := context.Background()
	service, _ := NewTestRedisService()
	flowAction := domain.FlowAction{
		WorkspaceId:  "TEST_WORKSPACE_ID",
		FlowId:       "flow_" + ksuid.New().String(),
		Id:           "flow_action_" + ksuid.New().String(),
		ActionType:   "testActionType",
		ActionStatus: domain.ActionStatusPending,
		ActionParams: map[string]interface{}{
			"key": "value",
		},
		IsHumanAction:    true,
		IsCallbackAction: false,
	}

	err := service.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	retrievedFlowAction, err := service.GetFlowAction(ctx, flowAction.WorkspaceId, flowAction.Id)
	assert.Nil(t, err)
	assert.Equal(t, flowAction, retrievedFlowAction)

	flowActions, err := service.GetFlowActions(ctx, flowAction.WorkspaceId, flowAction.FlowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)
	assert.Equal(t, flowAction, flowActions[0])

	// Check that the flow action ID was added to the Redis stream
	flowActionChanges, _, err := service.GetFlowActionChanges(ctx, flowAction.WorkspaceId, flowAction.FlowId, "0", 100, 0)
	assert.Nil(t, err)
	assert.Len(t, flowActionChanges, 1)
	assert.Equal(t, flowAction, flowActionChanges[0])
}

func TestPersistFlowAction_MissingId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisStorage()
	flowAction := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingFlowId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisStorage()
	flowAction := domain.FlowAction{
		Id:          "id_" + ksuid.New().String(),
		WorkspaceId: "TEST_WORKSPACE_ID",
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingWorkspaceId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisStorage()
	flowAction := domain.FlowAction{
		Id:     "id_" + ksuid.New().String(),
		FlowId: "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}
