package sqlite

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFlowActionStorage(t *testing.T) {
	storage := NewTestSqliteStorage(t, "flow_action_test")

	ctx := context.Background()
	workspaceId := "test-workspace"
	flowId := "test-flow"

	t.Run("PersistFlowAction", func(t *testing.T) {
		fa := domain.FlowAction{
			Id:                 "test-action-1",
			SubflowName:        "Test Subflow",
			SubflowDescription: "Test Description",
			SubflowId:          "test-subflow-1",
			FlowId:             flowId,
			WorkspaceId:        workspaceId,
			Created:            time.Now().UTC(),
			Updated:            time.Now().UTC(),
			ActionType:         "test-type",
			ActionParams:       map[string]interface{}{"key": "value"},
			ActionStatus:       "completed",
			ActionResult:       "test-result",
			IsHumanAction:      false,
			IsCallbackAction:   false,
		}

		err := storage.PersistFlowAction(ctx, fa)
		assert.NoError(t, err)
	})

	t.Run("GetFlowAction", func(t *testing.T) {
		fa, err := storage.GetFlowAction(ctx, workspaceId, "test-action-1")
		assert.NoError(t, err)
		assert.Equal(t, "test-action-1", fa.Id)
		assert.Equal(t, "Test Subflow", fa.SubflowName)
		assert.Equal(t, "test-subflow-1", fa.SubflowId)
		assert.Equal(t, flowId, fa.FlowId)
		assert.Equal(t, workspaceId, fa.WorkspaceId)
		assert.Equal(t, "test-type", fa.ActionType)
		assert.Equal(t, map[string]interface{}{"key": "value"}, fa.ActionParams)
		assert.Equal(t, "completed", fa.ActionStatus)
		assert.Equal(t, "test-result", fa.ActionResult)
		assert.False(t, fa.IsHumanAction)
		assert.False(t, fa.IsCallbackAction)
	})

	t.Run("GetFlowAction_NotFound", func(t *testing.T) {
		_, err := storage.GetFlowAction(ctx, workspaceId, "non-existent-id")
		assert.Equal(t, common.ErrNotFound, err)
	})

	t.Run("GetFlowActions", func(t *testing.T) {
		// Add another flow action
		fa2 := domain.FlowAction{
			Id:           "test-action-2",
			FlowId:       flowId,
			WorkspaceId:  workspaceId,
			Created:      time.Now().UTC(),
			Updated:      time.Now().UTC(),
			ActionType:   "test-type-2",
			ActionParams: map[string]interface{}{"key2": "value2"},
			ActionStatus: "pending",
		}
		err := storage.PersistFlowAction(ctx, fa2)
		assert.NoError(t, err)

		flowActions, err := storage.GetFlowActions(ctx, workspaceId, flowId)
		assert.NoError(t, err)
		assert.Len(t, flowActions, 2)

		// Check if both flow actions are present
		ids := []string{flowActions[0].Id, flowActions[1].Id}
		assert.Contains(t, ids, "test-action-1")
		assert.Contains(t, ids, "test-action-2")
	})

	t.Run("GetFlowActions_EmptyResult", func(t *testing.T) {
		flowActions, err := storage.GetFlowActions(ctx, workspaceId, "non-existent-flow")
		assert.NoError(t, err)
		assert.Empty(t, flowActions)
	})
}
