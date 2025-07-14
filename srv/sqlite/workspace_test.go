package sqlite

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPersistAndGetWorkspace(t *testing.T) {
	storage := NewTestStorage(t, "workspace_test")

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
	assert.Equal(t, common.ErrNotFound, err)

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
	storage := NewTestStorage(t, "workspace_test")
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
	storage := NewTestStorage(t, "workspace_test")
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
	assert.Equal(t, common.ErrNotFound, err)

	// Test deleting non-existent workspace
	err = storage.DeleteWorkspace(ctx, "non-existent-id")
	assert.Equal(t, common.ErrNotFound, err)
}

func TestPersistWorkspaceConfig(t *testing.T) {
	storage := NewTestStorage(t, "workspace_config_test")
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
	storage := NewTestStorage(t, "workspace_config_test")
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
