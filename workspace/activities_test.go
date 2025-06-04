package workspace

import (
	"context"
	"reflect"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetWorkspaceConfig(t *testing.T) {
	var db srv.Storage = sqlite.NewTestStorage(t, "get_workspace_config")
	activities := Activities{Storage: db}

	emptyConfig := domain.WorkspaceConfig{}
	testCases := []struct {
		name            string
		workspaceID     string
		workspaceConfig domain.WorkspaceConfig
		mockError       error
		expectError     bool
		errorMsg        string
	}{
		{
			name:        "Successful retrieval",
			workspaceID: "test-workspace-1",
			workspaceConfig: domain.WorkspaceConfig{
				LLM:       common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
				Embedding: common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
			},
			mockError:   nil,
			expectError: false,
		},
		{
			name:            "Missing workspace config",
			workspaceID:     "test-workspace-2",
			workspaceConfig: emptyConfig,
			expectError:     true,
			errorMsg:        srv.ErrNotFound.Error(),
		},
		{
			name:        "Missing LLM config",
			workspaceID: "test-workspace-3",
			workspaceConfig: domain.WorkspaceConfig{
				Embedding: common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
			},
		},
		{
			name:        "Missing embedding config",
			workspaceID: "test-workspace-4",
			workspaceConfig: domain.WorkspaceConfig{
				LLM: common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db.PersistWorkspace(ctx, domain.Workspace{Id: tc.workspaceID})
			if !reflect.DeepEqual(tc.workspaceConfig, emptyConfig) {
				db.PersistWorkspaceConfig(ctx, tc.workspaceID, tc.workspaceConfig)
			}

			config, err := activities.GetWorkspaceConfig(tc.workspaceID)

			if tc.expectError {
				assert.Error(t, err)
				if tc.mockError != nil {
					assert.Equal(t, tc.mockError, err)
				} else {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.workspaceConfig, config)
			}
		})
	}
}
