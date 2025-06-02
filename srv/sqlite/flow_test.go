package sqlite

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistFlow(t *testing.T) {
	storage := NewTestSqliteStorage(t, "flow_test")
	ctx := context.Background()

	t.Run("Insert new flow", func(t *testing.T) {
		flow := domain.Flow{
			WorkspaceId: "workspace1",
			Id:          "flow1",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent1",
			Status:      "active",
		}

		err := storage.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		// Verify the flow was inserted
		insertedFlow, err := storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)
		assert.Equal(t, flow, insertedFlow)
	})

	t.Run("Update existing flow", func(t *testing.T) {
		flow := domain.Flow{
			WorkspaceId: "workspace1",
			Id:          "flow1",
			Type:        domain.FlowTypePlannedDev,
			ParentId:    "parent2",
			Status:      "completed",
		}

		err := storage.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		// Verify the flow was updated
		updatedFlow, err := storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)
		assert.Equal(t, flow, updatedFlow)
	})
}

func TestGetFlow(t *testing.T) {
	storage := NewTestSqliteStorage(t, "flow_test")
	ctx := context.Background()

	t.Run("Get existing flow", func(t *testing.T) {
		expectedFlow := domain.Flow{
			WorkspaceId: "workspace1",
			Id:          "flow1",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent1",
			Status:      "active",
		}

		err := storage.PersistFlow(ctx, expectedFlow)
		require.NoError(t, err)

		retrievedFlow, err := storage.GetFlow(ctx, expectedFlow.WorkspaceId, expectedFlow.Id)
		assert.NoError(t, err)
		assert.Equal(t, expectedFlow, retrievedFlow)
	})

	t.Run("Get non-existent flow", func(t *testing.T) {
		_, err := storage.GetFlow(ctx, "non-existent-workspace", "non-existent-flow")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}

func TestGetFlowsForTask(t *testing.T) {
	storage := NewTestSqliteStorage(t, "flow_test")
	ctx := context.Background()

	t.Run("Get multiple flows for a task", func(t *testing.T) {
		workspaceId := "workspace1"
		taskId := "task1"
		expectedFlows := []domain.Flow{
			{WorkspaceId: workspaceId, Id: "flow1", Type: domain.FlowTypeBasicDev, ParentId: taskId, Status: "active"},
			{WorkspaceId: workspaceId, Id: "flow2", Type: domain.FlowTypePlannedDev, ParentId: taskId, Status: "completed"},
		}

		// Insert test flows
		for _, flow := range expectedFlows {
			err := storage.PersistFlow(ctx, flow)
			require.NoError(t, err)
		}

		// Get flows for the task
		flows, err := storage.GetFlowsForTask(ctx, workspaceId, taskId)
		assert.NoError(t, err)
		assert.Equal(t, expectedFlows, flows)
	})

	t.Run("Get flows for a task with no flows", func(t *testing.T) {
		workspaceId := "workspace2"
		taskId := "task2"

		flows, err := storage.GetFlowsForTask(ctx, workspaceId, taskId)
		assert.NoError(t, err)
		assert.Empty(t, flows)
	})

	t.Run("Error handling for database error", func(t *testing.T) {
		// Close the database connection to simulate a database error
		err := storage.db.Close()
		require.NoError(t, err)

		_, err = storage.GetFlowsForTask(ctx, "workspace3", "task3")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query flows for task")
	})
}
