package sqlite

import (
	"context"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("Insert new flow with zero timestamps", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		flow := domain.Flow{
			WorkspaceId: "workspace1",
			Id:          "flow1",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent1",
			Status:      "active",
		}

		err := storage.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		insertedFlow, err := storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)
		assert.Equal(t, flow.WorkspaceId, insertedFlow.WorkspaceId)
		assert.Equal(t, flow.Id, insertedFlow.Id)
		assert.Equal(t, flow.Type, insertedFlow.Type)
		assert.Equal(t, flow.ParentId, insertedFlow.ParentId)
		assert.Equal(t, flow.Status, insertedFlow.Status)
		assert.False(t, insertedFlow.Created.IsZero())
		assert.False(t, insertedFlow.Updated.IsZero())
		assert.Equal(t, time.UTC, insertedFlow.Created.Location())
		assert.Equal(t, time.UTC, insertedFlow.Updated.Location())
	})

	t.Run("Insert new flow with explicit timestamps preserves sub-millisecond precision", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		created := time.Date(2025, 6, 15, 10, 30, 45, 123456789, time.FixedZone("PST", -8*3600))
		updated := time.Date(2025, 6, 15, 11, 45, 30, 987654321, time.FixedZone("EST", -5*3600))
		flow := domain.Flow{
			WorkspaceId: "workspace2",
			Id:          "flow2",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent2",
			Status:      "active",
			Created:     created,
			Updated:     updated,
		}

		err := storage.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		insertedFlow, err := storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)
		assert.True(t, insertedFlow.Created.Equal(created.UTC()))
		assert.True(t, insertedFlow.Updated.Equal(updated.UTC()))
		assert.Equal(t, time.UTC, insertedFlow.Created.Location())
		assert.Equal(t, time.UTC, insertedFlow.Updated.Location())
		assert.Equal(t, 123456789, insertedFlow.Created.Nanosecond())
		assert.Equal(t, 987654321, insertedFlow.Updated.Nanosecond())
	})

	t.Run("Update existing flow", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		created := time.Now().UTC()
		flow := domain.Flow{
			WorkspaceId: "workspace3",
			Id:          "flow3",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent3",
			Status:      "active",
			Created:     created,
			Updated:     created,
		}

		err := storage.PersistFlow(ctx, flow)
		require.NoError(t, err)

		updated := time.Now().UTC().Add(time.Hour)
		flow.Type = domain.FlowTypePlannedDev
		flow.Status = "completed"
		flow.Updated = updated

		err = storage.PersistFlow(ctx, flow)
		assert.NoError(t, err)

		updatedFlow, err := storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)
		assert.Equal(t, domain.FlowTypePlannedDev, updatedFlow.Type)
		assert.Equal(t, "completed", updatedFlow.Status)
		assert.True(t, updatedFlow.Created.Equal(created))
		assert.True(t, updatedFlow.Updated.Equal(updated))
	})
}

func TestGetFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("Get existing flow", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		created := time.Date(2025, 1, 15, 12, 0, 0, 123456789, time.UTC)
		updated := time.Date(2025, 1, 15, 13, 0, 0, 987654321, time.UTC)
		expectedFlow := domain.Flow{
			WorkspaceId: "workspace_get1",
			Id:          "flow_get1",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent1",
			Status:      "active",
			Created:     created,
			Updated:     updated,
		}

		err := storage.PersistFlow(ctx, expectedFlow)
		require.NoError(t, err)

		retrievedFlow, err := storage.GetFlow(ctx, expectedFlow.WorkspaceId, expectedFlow.Id)
		assert.NoError(t, err)
		assert.Equal(t, expectedFlow.WorkspaceId, retrievedFlow.WorkspaceId)
		assert.Equal(t, expectedFlow.Id, retrievedFlow.Id)
		assert.Equal(t, expectedFlow.Type, retrievedFlow.Type)
		assert.Equal(t, expectedFlow.ParentId, retrievedFlow.ParentId)
		assert.Equal(t, expectedFlow.Status, retrievedFlow.Status)
		assert.True(t, retrievedFlow.Created.Equal(created))
		assert.True(t, retrievedFlow.Updated.Equal(updated))
	})

	t.Run("Get non-existent flow", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		_, err := storage.GetFlow(ctx, "non-existent-workspace", "non-existent-flow")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}

func TestDeleteFlow(t *testing.T) {
	storage := NewTestSqliteStorage(t, "flow_test")
	ctx := context.Background()

	t.Run("Delete existing flow", func(t *testing.T) {
		flow := domain.Flow{
			WorkspaceId: "workspace1",
			Id:          "flow-to-delete",
			Type:        domain.FlowTypeBasicDev,
			ParentId:    "parent1",
			Status:      "active",
		}
		err := storage.PersistFlow(ctx, flow)
		require.NoError(t, err)

		// Verify flow exists
		_, err = storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		require.NoError(t, err)

		// Delete the flow
		err = storage.DeleteFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.NoError(t, err)

		// Verify flow is deleted
		_, err = storage.GetFlow(ctx, flow.WorkspaceId, flow.Id)
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("Delete non-existent flow is idempotent", func(t *testing.T) {
		err := storage.DeleteFlow(ctx, "workspace1", "non-existent-flow")
		assert.NoError(t, err)
	})
}

func TestGetFlowsForTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("Get multiple flows for a task with timestamps", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		workspaceId := "workspace_task1"
		taskId := "task_flows1"
		created1 := time.Date(2025, 2, 10, 8, 0, 0, 111222333, time.UTC)
		updated1 := time.Date(2025, 2, 10, 9, 0, 0, 444555666, time.UTC)
		created2 := time.Date(2025, 2, 11, 10, 0, 0, 777888999, time.UTC)
		updated2 := time.Date(2025, 2, 11, 11, 0, 0, 123123123, time.UTC)

		expectedFlows := []domain.Flow{
			{WorkspaceId: workspaceId, Id: "flow_task1", Type: domain.FlowTypeBasicDev, ParentId: taskId, Status: "active", Created: created1, Updated: updated1},
			{WorkspaceId: workspaceId, Id: "flow_task2", Type: domain.FlowTypePlannedDev, ParentId: taskId, Status: "completed", Created: created2, Updated: updated2},
		}

		for _, flow := range expectedFlows {
			err := storage.PersistFlow(ctx, flow)
			require.NoError(t, err)
		}

		flows, err := storage.GetFlowsForTask(ctx, workspaceId, taskId)
		assert.NoError(t, err)
		require.Len(t, flows, 2)

		for i, flow := range flows {
			assert.Equal(t, expectedFlows[i].WorkspaceId, flow.WorkspaceId)
			assert.Equal(t, expectedFlows[i].Id, flow.Id)
			assert.Equal(t, expectedFlows[i].Type, flow.Type)
			assert.Equal(t, expectedFlows[i].ParentId, flow.ParentId)
			assert.Equal(t, expectedFlows[i].Status, flow.Status)
			assert.True(t, flow.Created.Equal(expectedFlows[i].Created))
			assert.True(t, flow.Updated.Equal(expectedFlows[i].Updated))
			assert.Equal(t, time.UTC, flow.Created.Location())
			assert.Equal(t, time.UTC, flow.Updated.Location())
		}
	})

	t.Run("Get flows for a task with no flows", func(t *testing.T) {
		t.Parallel()
		storage := NewTestSqliteStorage(t, "flow_test")
		workspaceId := "workspace_task2"
		taskId := "task_noflows"

		flows, err := storage.GetFlowsForTask(ctx, workspaceId, taskId)
		assert.NoError(t, err)
		assert.Empty(t, flows)
	})

	t.Run("Error handling for database error", func(t *testing.T) {
		closedStorage := NewTestSqliteStorage(t, "flow_test_closed")
		err := closedStorage.db.Close()
		require.NoError(t, err)

		_, err = closedStorage.GetFlowsForTask(ctx, "workspace3", "task3")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query flows for task")
	})
}
