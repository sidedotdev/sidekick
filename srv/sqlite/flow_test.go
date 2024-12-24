package sqlite

import (
	"context"
	"testing"

	"sidekick/domain"
	"sidekick/srv"

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
		assert.ErrorIs(t, err, srv.ErrNotFound)
	})
}
