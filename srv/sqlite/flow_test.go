package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"sidekick/domain"
	"sidekick/srv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistFlow(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = runMigrations(db)
	require.NoError(t, err)

	storage := NewStorage(db)

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
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM flows WHERE workspace_id = ? AND id = ?", flow.WorkspaceId, flow.Id).Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
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
		var updatedFlow domain.Flow
		err = db.QueryRow("SELECT workspace_id, id, type, parent_id, status FROM flows WHERE workspace_id = ? AND id = ?",
			flow.WorkspaceId, flow.Id).Scan(&updatedFlow.WorkspaceId, &updatedFlow.Id, &updatedFlow.Type, &updatedFlow.ParentId, &updatedFlow.Status)
		assert.NoError(t, err)
		assert.Equal(t, flow, updatedFlow)
	})
}

func runMigrations(db *sql.DB) error {
	// TODO: Implement proper migration runner
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS flows (
			workspace_id TEXT NOT NULL,
			id TEXT NOT NULL,
			type TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			status TEXT NOT NULL,
			PRIMARY KEY (workspace_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_flows_parent_id ON flows(workspace_id, parent_id);
	`)
	return err
}

func TestGetFlow(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = runMigrations(db)
	require.NoError(t, err)

	storage := NewStorage(db)

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
