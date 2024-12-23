package redis

import (
	"context"
	"fmt"
	"sidekick/domain"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestGetWorkflows(t *testing.T) {
	db := NewTestRedisDatabase()

	workspaceId := "TEST_WORKSPACE_ID"
	parentId := "testParentId"
	flows := []domain.Flow{
		{
			WorkspaceId: workspaceId,
			Id:          "workflow_" + ksuid.New().String(),
			Type:        "testType1",
			ParentId:    parentId,
			Status:      "testStatus1",
		},
		{
			WorkspaceId: workspaceId,
			Id:          "workflow_" + ksuid.New().String(),
			Type:        "testType2",
			ParentId:    parentId,
			Status:      "testStatus2",
		},
	}

	for _, flow := range flows {
		err := db.PersistWorkflow(context.Background(), flow)
		assert.Nil(t, err)
	}

	retrievedWorkflows, err := db.GetFlowsForTask(context.Background(), workspaceId, parentId)
	assert.Nil(t, err)
	assert.Equal(t, flows, retrievedWorkflows)
}

func TestPersistSubflow(t *testing.T) {
	db := NewTestRedisDatabase()
	ctx := context.Background()

	validSubflow := domain.Subflow{
		WorkspaceId: ksuid.New().String(),
		Id:          "sf_" + ksuid.New().String(),
		FlowId:      ksuid.New().String(),
		Name:        "Test Subflow",
		Description: "This is a test subflow",
		Status:      domain.SubflowStatusInProgress,
	}

	tests := []struct {
		name          string
		subflow       domain.Subflow
		expectedError bool
		errorContains string
	}{
		{
			name:          "Successfully persist a valid subflow",
			subflow:       validSubflow,
			expectedError: false,
		},
		{
			name: "Empty WorkspaceId",
			subflow: func() domain.Subflow {
				sf := validSubflow
				sf.WorkspaceId = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "workspaceId",
		},
		{
			name: "Empty Id",
			subflow: func() domain.Subflow {
				sf := validSubflow
				sf.Id = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "subflow.Id",
		},
		{
			name: "Empty FlowId",
			subflow: func() domain.Subflow {
				sf := validSubflow
				sf.FlowId = ""
				return sf
			}(),
			expectedError: true,
			errorContains: "subflow.FlowId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.PersistSubflow(ctx, tt.subflow)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)

				// Verify the subflow was persisted correctly
				subflowKey := fmt.Sprintf("%s:%s", tt.subflow.WorkspaceId, tt.subflow.Id)
				subflowSetKey := fmt.Sprintf("%s:%s:subflows", tt.subflow.WorkspaceId, tt.subflow.FlowId)

				// Check if the subflow exists in Redis
				exists, err := db.Client.Exists(ctx, subflowKey).Result()
				assert.NoError(t, err)
				assert.Equal(t, int64(1), exists)

				// Check if the subflow ID is in the flow's subflow set
				isMember, err := db.Client.SIsMember(ctx, subflowSetKey, tt.subflow.Id).Result()
				assert.NoError(t, err)
				assert.True(t, isMember)
			}
		})
	}
}

func TestGetSubflows(t *testing.T) {
	db := NewTestRedisDatabase()
	ctx := context.Background()

	workspaceId := ksuid.New().String()
	flowId := ksuid.New().String()

	// Create test subflows
	subflows := []domain.Subflow{
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			Name:        "Subflow 1",
			FlowId:      flowId,
			Status:      domain.SubflowStatusInProgress,
		},
		{
			WorkspaceId: workspaceId,
			Id:          "sf_" + ksuid.New().String(),
			Name:        "Subflow 2",
			FlowId:      flowId,
			Status:      domain.SubflowStatusComplete,
		},
	}

	// Persist test subflows
	for _, sf := range subflows {
		err := db.PersistSubflow(ctx, sf)
		assert.NoError(t, err)
	}

	tests := []struct {
		name           string
		workspaceId    string
		flowId         string
		expectedError  bool
		errorContains  string
		expectedLength int
	}{
		{
			name:           "Successfully retrieving multiple subflows",
			workspaceId:    workspaceId,
			flowId:         flowId,
			expectedError:  false,
			expectedLength: 2,
		},
		{
			name:          "Empty workspaceId",
			workspaceId:   "",
			flowId:        flowId,
			expectedError: true,
			errorContains: "workspaceId and flowId cannot be empty",
		},
		{
			name:          "Empty flowId",
			workspaceId:   workspaceId,
			flowId:        "",
			expectedError: true,
			errorContains: "workspaceId and flowId cannot be empty",
		},
		{
			name:           "Non-existent flow",
			workspaceId:    workspaceId,
			flowId:         ksuid.New().String(),
			expectedError:  false,
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retrievedSubflows, err := db.GetSubflows(ctx, tt.workspaceId, tt.flowId)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Len(t, retrievedSubflows, tt.expectedLength)

				if tt.expectedLength > 0 {
					assert.ElementsMatch(t, subflows, retrievedSubflows)
				}
			}
		})
	}
}

func TestGetFlowActions(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisDatabase()
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
	db := NewTestRedisDatabase()
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

	err := db.PersistFlowAction(ctx, flowAction)
	assert.Nil(t, err)

	retrievedFlowAction, err := db.GetFlowAction(ctx, flowAction.WorkspaceId, flowAction.Id)
	assert.Nil(t, err)
	assert.Equal(t, flowAction, retrievedFlowAction)

	flowActions, err := db.GetFlowActions(ctx, flowAction.WorkspaceId, flowAction.FlowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)
	assert.Equal(t, flowAction, flowActions[0])

	// Check that the flow action ID was added to the Redis stream
	flowActionChanges, _, err := db.GetFlowActionChanges(ctx, flowAction.WorkspaceId, flowAction.FlowId, "0", 100, 0)
	assert.Nil(t, err)
	assert.Len(t, flowActionChanges, 1)
	assert.Equal(t, flowAction, flowActionChanges[0])
}

func TestPersistFlowAction_MissingId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisDatabase()
	flowAction := domain.FlowAction{
		WorkspaceId: "TEST_WORKSPACE_ID",
		FlowId:      "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingFlowId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisDatabase()
	flowAction := domain.FlowAction{
		Id:          "id_" + ksuid.New().String(),
		WorkspaceId: "TEST_WORKSPACE_ID",
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}

func TestPersistFlowAction_MissingWorkspaceId(t *testing.T) {
	ctx := context.Background()
	db := NewTestRedisDatabase()
	flowAction := domain.FlowAction{
		Id:     "id_" + ksuid.New().String(),
		FlowId: "flow_" + ksuid.New().String(),
	}

	err := db.PersistFlowAction(ctx, flowAction)
	assert.NotNil(t, err)
}
