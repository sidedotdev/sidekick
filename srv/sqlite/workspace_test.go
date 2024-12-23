package sqlite

import (
	"context"
	"sidekick/domain"
	"sidekick/srv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPersistAndGetWorkspace(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_test")

	ctx := context.Background()

	workspace := domain.Workspace{
		Id:           "test-workspace-id",
		Name:         "Test Workspace",
		LocalRepoDir: "/path/to/repo",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}

	// Test PersistWorkspace
	err := storage.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err)

	// Test GetWorkspace
	retrievedWorkspace, err := storage.GetWorkspace(ctx, workspace.Id)
	assert.NoError(t, err)
	assert.Equal(t, workspace, retrievedWorkspace)

	// Test GetWorkspace with non-existent ID
	_, err = storage.GetWorkspace(ctx, "non-existent-id")
	assert.Equal(t, srv.ErrNotFound, err)

	// Test updating an existing workspace
	updatedWorkspace := workspace
	updatedWorkspace.Name = "Updated Test Workspace"
	updatedWorkspace.Updated = time.Now().UTC().Truncate(time.Millisecond)

	err = storage.PersistWorkspace(ctx, updatedWorkspace)
	assert.NoError(t, err)

	retrievedUpdatedWorkspace, err := storage.GetWorkspace(ctx, updatedWorkspace.Id)
	assert.NoError(t, err)
	assert.Equal(t, updatedWorkspace, retrievedUpdatedWorkspace)
}
