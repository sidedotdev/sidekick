package workspace

import (
	"context"
	"reflect"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorkspaceConfig(t *testing.T) {
	db := sqlite.NewTestSqliteStorage(t, "workspace_activities_test")
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

			// Ensure workspace exists for the test
			err := db.PersistWorkspace(ctx, domain.Workspace{
				Id:      tc.workspaceID,
				Name:    "test-workspace",
				Created: time.Now(),
				Updated: time.Now(),
			})
			require.NoError(t, err)

			if !reflect.DeepEqual(tc.workspaceConfig, emptyConfig) {
				err = db.PersistWorkspaceConfig(ctx, tc.workspaceID, tc.workspaceConfig)
				require.NoError(t, err)
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
