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

func TestGetAllWorkspaces(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_test")
	ctx := context.Background()

	// Create test workspaces
	workspaces := []domain.Workspace{
		{Id: "ws-2", Name: "Workspace B", LocalRepoDir: "/path/b", Created: time.Now().UTC().Truncate(time.Millisecond), Updated: time.Now().UTC().Truncate(time.Millisecond)},
		{Id: "ws-1", Name: "Workspace A", LocalRepoDir: "/path/a", Created: time.Now().UTC().Truncate(time.Millisecond), Updated: time.Now().UTC().Truncate(time.Millisecond)},
		{Id: "ws-3", Name: "Workspace C", LocalRepoDir: "/path/c", Created: time.Now().UTC().Truncate(time.Millisecond), Updated: time.Now().UTC().Truncate(time.Millisecond)},
	}

	for _, w := range workspaces {
		err := storage.PersistWorkspace(ctx, w)
		assert.NoError(t, err)
	}

	// Test GetAllWorkspaces
	retrievedWorkspaces, err := storage.GetAllWorkspaces(ctx)
	assert.NoError(t, err)
	assert.Len(t, retrievedWorkspaces, 3)

	// Check if workspaces are sorted by name
	assert.Equal(t, "Workspace A", retrievedWorkspaces[0].Name)
	assert.Equal(t, "Workspace B", retrievedWorkspaces[1].Name)
	assert.Equal(t, "Workspace C", retrievedWorkspaces[2].Name)
}

func TestDeleteWorkspace(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_test")
	ctx := context.Background()

	workspace := domain.Workspace{
		Id:           "test-delete-id",
		Name:         "Test Delete Workspace",
		LocalRepoDir: "/test/delete/path",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}

	err := storage.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err)

	// Test deleting existing workspace
	err = storage.DeleteWorkspace(ctx, workspace.Id)
	assert.NoError(t, err)

	// Verify workspace is deleted
	_, err = storage.GetWorkspace(ctx, workspace.Id)
	assert.Equal(t, srv.ErrNotFound, err)

	// Test deleting non-existent workspace
	err = storage.DeleteWorkspace(ctx, "non-existent-id")
	assert.Equal(t, srv.ErrNotFound, err)
}
