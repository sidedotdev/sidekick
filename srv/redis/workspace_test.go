package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPersistWorkspace(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorageT(t)
	workspace := domain.Workspace{
		Id:         "123",
		Name:       "TestWorkspace",
		ConfigMode: "merge",
	}

	// Test will try to persist the workspace
	err := db.PersistWorkspace(ctx, workspace)
	if err != nil {
		t.Fatalf("failed to persist workspace: %v", err)
	}

	// Verifying storage in original data structure
	key := fmt.Sprintf("workspace:%s", workspace.Id)
	workspaceJson, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		t.Errorf("failed to get workspace from redis: %v", err)
	}

	var persistedWorkspace domain.Workspace
	if err := json.Unmarshal([]byte(workspaceJson), &persistedWorkspace); err != nil {
		t.Errorf("failed to unmarshal workspace JSON: %v", err)
	}

	if persistedWorkspace.Name != workspace.Name {
		t.Errorf("expected workspace name %s, got %s", workspace.Name, persistedWorkspace.Name)
	}

	if persistedWorkspace.ConfigMode != workspace.ConfigMode {
		t.Errorf("expected workspace configMode %s, got %s", workspace.ConfigMode, persistedWorkspace.ConfigMode)
	}

	// Verifying storage in new sorted set structure
	workspaceNameIds, err := db.Client.ZRange(ctx, "global:workspaces", 0, -1).Result()
	assert.Nil(t, err)
	if len(workspaceNameIds) != 1 {
		t.Errorf("expected 1 workspace in global:workspaces, got %d", len(workspaceNameIds))
	}
	if workspaceNameIds[0] != workspace.Name+":"+workspace.Id {
		t.Errorf("expected workspace id %s, got %s", workspace.Id, workspaceNameIds[0])
	}

	workspaces, err := db.GetAllWorkspaces(ctx)
	assert.Nil(t, err)
	if workspaces[0].Id != workspace.Id {
		t.Errorf("expected workspace id %s, got %s", workspace.Id, workspaces[0].Id)
	}
	if workspaces[0].ConfigMode != workspace.ConfigMode {
		t.Errorf("expected workspace configMode %s, got %s", workspace.ConfigMode, workspaces[0].ConfigMode)
	}
}

func TestPersistWorkspaceConfig(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorageT(t)
	workspaceId := "test-workspace-id"

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

	// Test persisting the config
	err := db.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Retrieve the config and verify
	retrievedConfig, err := db.GetWorkspaceConfig(ctx, workspaceId)
	assert.NoError(t, err)
	assert.Equal(t, config, retrievedConfig)

	// Test persisting with an empty workspaceId
	err = db.PersistWorkspaceConfig(ctx, "", config)
	assert.Error(t, err)
	assert.EqualError(t, err, "workspaceId cannot be empty")
}

func TestGetWorkspaceConfig(t *testing.T) {
	ctx := context.Background()
	s := newTestRedisStorageT(t)
	workspaceId := "test-workspace-id"

	// Test retrieving a non-existent config
	_, err := s.GetWorkspaceConfig(ctx, workspaceId)
	assert.Error(t, err)
	assert.ErrorIs(t, err, srv.ErrNotFound)

	// Create and persist a config
	config := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "gpt-3.5-turbo"},
			},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{
				{Provider: "OpenAI", Model: "text-embedding-ada-002"},
			},
		},
	}
	err = s.PersistWorkspaceConfig(ctx, workspaceId, config)
	assert.NoError(t, err)

	// Test retrieving the existing config
	retrievedConfig, err := s.GetWorkspaceConfig(ctx, workspaceId)
	assert.NoError(t, err)
	assert.Equal(t, config, retrievedConfig)

	// Test retrieving with an empty workspaceId
	_, err = s.GetWorkspaceConfig(ctx, "")
	assert.Error(t, err)
}
