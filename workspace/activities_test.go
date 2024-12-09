package workspace

import (
	"context"
	"log"
	"reflect"
	"sidekick/common"
	sidedb "sidekick/db"
	"sidekick/models"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func newTestRedisDatabase() *sidedb.RedisDatabase {
	db := &sidedb.RedisDatabase{}
	db.Client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})

	// Flush the database synchronously to ensure a clean state for each test
	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return db
}

func TestGetWorkspaceConfig(t *testing.T) {
	db := newTestRedisDatabase()
	activities := Activities{DatabaseAccessor: db}

	emptyConfig := models.WorkspaceConfig{}
	testCases := []struct {
		name            string
		workspaceID     string
		workspaceConfig models.WorkspaceConfig
		mockError       error
		expectError     bool
		errorMsg        string
	}{
		{
			name:        "Successful retrieval",
			workspaceID: "test-workspace-1",
			workspaceConfig: models.WorkspaceConfig{
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
			errorMsg:        sidedb.ErrNotFound.Error(),
		},
		{
			name:        "Missing LLM config",
			workspaceID: "test-workspace-3",
			workspaceConfig: models.WorkspaceConfig{
				Embedding: common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
			},
			mockError:   nil,
			expectError: true,
			errorMsg:    "missing LLM config",
		},
		{
			name:        "Missing embedding config",
			workspaceID: "test-workspace-4",
			workspaceConfig: models.WorkspaceConfig{
				LLM: common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
			},
			mockError:   nil,
			expectError: true,
			errorMsg:    "missing embedding config",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
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
