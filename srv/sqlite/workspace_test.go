package sqlite

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistAndGetWorkspace(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_test")

	ctx := context.Background()

	workspace := domain.Workspace{
		Id:           "test-workspace-id",
		Name:         "Test Workspace",
		LocalRepoDir: "/path/to/repo",
		Created:      time.Now().UTC(),
		Updated:      time.Now().UTC(),
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
	assert.Equal(t, common.ErrNotFound, err)

	// Test updating an existing workspace
	updatedWorkspace := workspace
	updatedWorkspace.Name = "Updated Test Workspace"
	updatedWorkspace.Updated = time.Now().UTC()

	err = storage.PersistWorkspace(ctx, updatedWorkspace)
	assert.NoError(t, err)

	retrievedUpdatedWorkspace, err := storage.GetWorkspace(ctx, updatedWorkspace.Id)
	assert.NoError(t, err)
	assert.Equal(t, updatedWorkspace, retrievedUpdatedWorkspace)
}

func TestWorkspaceProfilePersistence(t *testing.T) {
	ctx := context.Background()

	t.Run("persists an assigned profile", func(t *testing.T) {
		storage := NewTestSqliteStorage(t, "workspace_profile_test")
		workspace := domain.Workspace{
			Id:           "ws-profile",
			Name:         "Profile Workspace",
			LocalRepoDir: "/path/to/repo",
			ConfigMode:   "merge",
			ProfileId:    "work",
			Created:      time.Now().UTC(),
			Updated:      time.Now().UTC(),
		}

		require.NoError(t, storage.PersistWorkspace(ctx, workspace))

		retrieved, err := storage.GetWorkspace(ctx, workspace.Id)
		require.NoError(t, err)
		assert.Equal(t, "work", retrieved.ProfileId)
		assert.Equal(t, "work", retrieved.EffectiveProfileId())

		all, err := storage.GetAllWorkspaces(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Equal(t, "work", all[0].ProfileId)
	})

	t.Run("persists an unset profile as null and reads it as the default profile", func(t *testing.T) {
		storage := NewTestSqliteStorage(t, "workspace_profile_test")
		workspace := domain.Workspace{
			Id:           "ws-no-profile",
			Name:         "No Profile Workspace",
			LocalRepoDir: "/path/to/repo",
			ConfigMode:   "merge",
			Created:      time.Now().UTC(),
			Updated:      time.Now().UTC(),
		}

		require.NoError(t, storage.PersistWorkspace(ctx, workspace))
		assert.True(t, storedProfileIdIsNull(t, storage, workspace.Id))

		retrieved, err := storage.GetWorkspace(ctx, workspace.Id)
		require.NoError(t, err)
		assert.Empty(t, retrieved.ProfileId)
		assert.Equal(t, common.DefaultProfileId, retrieved.EffectiveProfileId())

		all, err := storage.GetAllWorkspaces(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Empty(t, all[0].ProfileId)
		assert.Equal(t, common.DefaultProfileId, all[0].EffectiveProfileId())
	})

	t.Run("clears a previously assigned profile", func(t *testing.T) {
		storage := NewTestSqliteStorage(t, "workspace_profile_test")
		workspace := domain.Workspace{
			Id:           "ws-cleared-profile",
			Name:         "Cleared Profile Workspace",
			LocalRepoDir: "/path/to/repo",
			ConfigMode:   "merge",
			ProfileId:    "work",
			Created:      time.Now().UTC(),
			Updated:      time.Now().UTC(),
		}
		require.NoError(t, storage.PersistWorkspace(ctx, workspace))

		workspace.ProfileId = ""
		require.NoError(t, storage.PersistWorkspace(ctx, workspace))
		assert.True(t, storedProfileIdIsNull(t, storage, workspace.Id))

		retrieved, err := storage.GetWorkspace(ctx, workspace.Id)
		require.NoError(t, err)
		assert.Empty(t, retrieved.ProfileId)
	})

	t.Run("reads records written before profiles existed", func(t *testing.T) {
		storage := NewTestSqliteStorage(t, "workspace_profile_test")
		now := time.Now().UTC()
		_, err := storage.db.ExecContext(ctx, `
			INSERT INTO workspaces (id, name, local_repo_dir, config_mode, created, updated)
			VALUES (?, ?, ?, ?, ?, ?)
		`, "ws-legacy", "Legacy Workspace", "/path/to/repo", "merge", now, now)
		require.NoError(t, err)
		assert.True(t, storedProfileIdIsNull(t, storage, "ws-legacy"))

		retrieved, err := storage.GetWorkspace(ctx, "ws-legacy")
		require.NoError(t, err)
		assert.Empty(t, retrieved.ProfileId)
		assert.Equal(t, common.DefaultProfileId, retrieved.EffectiveProfileId())

		all, err := storage.GetAllWorkspaces(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Empty(t, all[0].ProfileId)
		assert.Equal(t, common.DefaultProfileId, all[0].EffectiveProfileId())
	})
}

func storedProfileIdIsNull(t *testing.T, storage *Storage, workspaceId string) bool {
	t.Helper()
	var isNull bool
	err := storage.db.QueryRowContext(context.Background(),
		"SELECT profile_id IS NULL FROM workspaces WHERE id = ?", workspaceId).Scan(&isNull)
	require.NoError(t, err)
	return isNull
}

func TestGetAllWorkspaces(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_test")
	ctx := context.Background()

	// Create test workspaces
	workspaces := []domain.Workspace{
		{Id: "ws-2", Name: "Workspace B", LocalRepoDir: "/path/b", Created: time.Now().UTC(), Updated: time.Now().UTC()},
		{Id: "ws-1", Name: "Workspace A", LocalRepoDir: "/path/a", Created: time.Now().UTC(), Updated: time.Now().UTC()},
		{Id: "ws-3", Name: "Workspace C", LocalRepoDir: "/path/c", Created: time.Now().UTC(), Updated: time.Now().UTC()},
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
		Created:      time.Now().UTC(),
		Updated:      time.Now().UTC(),
	}

	err := storage.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err)

	// Test deleting existing workspace
	err = storage.DeleteWorkspace(ctx, workspace.Id)
	assert.NoError(t, err)

	// Verify workspace is deleted
	_, err = storage.GetWorkspace(ctx, workspace.Id)
	assert.Equal(t, common.ErrNotFound, err)

	// Test deleting non-existent workspace
	err = storage.DeleteWorkspace(ctx, "non-existent-id")
	assert.Equal(t, common.ErrNotFound, err)
}

func TestPersistWorkspaceConfig(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_config_test")
	ctx := context.Background()

	workspaceId := "test-config-workspace-id"
	workspace := domain.Workspace{
		Id:           workspaceId,
		Name:         "Test Config Workspace",
		LocalRepoDir: "/test/config/path",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}

	err := storage.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err)

	config := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "gpt-3.5-turbo"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{
				"summarization": {{Provider: "OpenAI", Model: "gpt-4"}},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "text-embedding-ada-002"},
			},
		},
	}

	// Test PersistWorkspaceConfig
	err = storage.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Test updating existing config
	updatedConfig := config
	updatedConfig.LLM.Defaults[0].Model = "gpt-4"
	err = storage.PersistWorkspaceConfig(ctx, workspaceId, updatedConfig)
	assert.NoError(t, err)

	// Test PersistWorkspaceConfig with non-existent workspace
	err = storage.PersistWorkspaceConfig(ctx, "non-existent-id", config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace not found")
}

func TestGetWorkspaceConfig(t *testing.T) {
	storage := NewTestSqliteStorage(t, "workspace_config_test")
	ctx := context.Background()

	workspaceId := "test-config-workspace-id"
	workspace := domain.Workspace{
		Id:           workspaceId,
		Name:         "Test Config Workspace",
		LocalRepoDir: "/test/config/path",
		Created:      time.Now().UTC().Truncate(time.Millisecond),
		Updated:      time.Now().UTC().Truncate(time.Millisecond),
	}

	err := storage.PersistWorkspace(ctx, workspace)
	assert.NoError(t, err)

	config := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "gpt-3.5-turbo"},
			},
			UseCaseConfigs: map[string][]common.ModelConfig{
				"summarization": {{Provider: "OpenAI", Model: "gpt-4"}},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "text-embedding-ada-002"},
			},
		},
	}

	err = storage.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Test GetWorkspaceConfig
	retrievedConfig, err := storage.GetWorkspaceConfig(ctx, workspaceId)
	assert.NoError(t, err)
	assert.Equal(t, config, retrievedConfig)

	// Test GetWorkspaceConfig with non-existent workspace
	_, err = storage.GetWorkspaceConfig(ctx, "non-existent-id")
	assert.Equal(t, common.ErrNotFound, err)
}
