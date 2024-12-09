package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/common"
	"sidekick/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestUpdateWorkspaceHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.dbAccessor

	testCases := []struct {
		name              string
		workspaceId       string
		workspaceRequest  WorkspaceRequest
		expectedStatus    int
		expectedWorkspace *models.Workspace
		expectedConfig    *models.WorkspaceConfig
		expectedError     string
	}{
		{
			name:        "Valid workspace update",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &models.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Updated Workspace",
				LocalRepoDir: "/new/path/to/repo",
			},
			expectedConfig: &models.WorkspaceConfig{
				LLM: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				Embedding: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
		},
		{
			name:        "Update workspace config only",
			workspaceId: "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedWorkspace: &models.Workspace{
				Id:           "existing_workspace_id",
				Name:         "Initial Workspace",
				LocalRepoDir: "/path/to/repo",
			},
			expectedConfig: &models.WorkspaceConfig{
				LLM: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "anthropic", Model: "claude-v1"}},
				},
				Embedding: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "cohere", Model: "embed-english-v2.0"}},
				},
			},
		},
		{
			name:             "Missing workspace name and local repo dir",
			workspaceId:      "existing_workspace_id",
			workspaceRequest: WorkspaceRequest{},
			expectedStatus:   http.StatusBadRequest,
			expectedError:    "At least one of Name, LocalRepoDir, LLMConfig, or EmbeddingConfig is required",
		},
		{
			name:             "Workspace not found",
			workspaceId:      "non_existing_workspace_id",
			workspaceRequest: WorkspaceRequest{Name: "Updated Workspace", LocalRepoDir: "/new/path/to/repo"},
			expectedStatus:   http.StatusNotFound,
			expectedError:    "not found",
		},
	}

	for _, tc := range testCases {
		// Setup initial workspace data, must do for each test case to ensure we
		// have a clean start
		initialWorkspace := &models.Workspace{
			Id:           "existing_workspace_id",
			Name:         "Initial Workspace",
			LocalRepoDir: "/path/to/repo",
		}
		err := db.PersistWorkspace(context.Background(), *initialWorkspace)
		assert.NoError(t, err)

		// Setup initial workspace config
		initialConfig := &models.WorkspaceConfig{
			LLM: common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-3.5-turbo"}},
			},
			Embedding: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
			},
		}
		err = db.PersistWorkspaceConfig(context.Background(), initialWorkspace.Id, *initialConfig)
		assert.NoError(t, err)

		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)

			jsonData, err := json.Marshal(tc.workspaceRequest)
			assert.NoError(t, err)

			route := "/v1/workspaces/" + tc.workspaceId
			c.Request = httptest.NewRequest("PUT", route, bytes.NewBuffer(jsonData))
			c.Params = gin.Params{{Key: "workspaceId", Value: tc.workspaceId}}
			ctrl.UpdateWorkspaceHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if resp.Code == http.StatusOK {
				var responseBody struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &responseBody)
				assert.NoError(t, err)

				assert.Equal(t, tc.expectedWorkspace.Id, responseBody.Workspace.Id)
				assert.Equal(t, tc.expectedWorkspace.Name, responseBody.Workspace.Name)
				assert.Equal(t, tc.expectedWorkspace.LocalRepoDir, responseBody.Workspace.LocalRepoDir)

				if tc.expectedConfig != nil {
					assert.Equal(t, tc.expectedConfig.LLM.Defaults, responseBody.Workspace.LLMConfig.Defaults)
					assert.Equal(t, tc.expectedConfig.Embedding.Defaults, responseBody.Workspace.EmbeddingConfig.Defaults)
				}
			} else {
				responseBody := make(map[string]string)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				assert.Equal(t, tc.expectedError, responseBody["error"])
			}
		})
	}
}

func TestCreateWorkspaceHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)

	testCases := []struct {
		name             string
		workspaceRequest WorkspaceRequest
		expectedStatus   int
		expectedResponse *WorkspaceResponse
		expectedError    string
	}{
		{
			name: "Valid workspace creation",
			workspaceRequest: WorkspaceRequest{
				Name:         "New Workspace",
				LocalRepoDir: "/path/to/new/repo",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
			expectedStatus: http.StatusOK,
			expectedResponse: &WorkspaceResponse{
				Name:         "New Workspace",
				LocalRepoDir: "/path/to/new/repo",
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
				},
			},
		},
		{
			name: "Invalid workspace creation - missing name",
			workspaceRequest: WorkspaceRequest{
				LocalRepoDir: "/path/to/new/repo",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Name is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)

			jsonData, err := json.Marshal(tc.workspaceRequest)
			assert.NoError(t, err)

			c.Request = httptest.NewRequest("POST", "/v1/workspaces", bytes.NewBuffer(jsonData))
			ctrl.CreateWorkspaceHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if resp.Code == http.StatusOK {
				var responseBody struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &responseBody)
				assert.NoError(t, err)

				assert.NotEmpty(t, responseBody.Workspace.Id)
				assert.Equal(t, tc.expectedResponse.Name, responseBody.Workspace.Name)
				assert.Equal(t, tc.expectedResponse.LocalRepoDir, responseBody.Workspace.LocalRepoDir)
				assert.Equal(t, tc.expectedResponse.LLMConfig, responseBody.Workspace.LLMConfig)
				assert.Equal(t, tc.expectedResponse.EmbeddingConfig, responseBody.Workspace.EmbeddingConfig)
				assert.NotZero(t, responseBody.Workspace.Created)
				assert.NotZero(t, responseBody.Workspace.Updated)
			} else {
				responseBody := make(map[string]string)
				json.Unmarshal(resp.Body.Bytes(), &responseBody)

				assert.Equal(t, tc.expectedError, responseBody["error"])
			}
		})
	}
}

func TestGetWorkspaceByIdHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.dbAccessor

	// Setup workspaces and configs
	workspace1 := models.Workspace{Id: "workspace1", Name: "Workspace One", LocalRepoDir: "/path/to/repo1"}
	config1 := models.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "gpt-4"}},
		},
		Embedding: common.EmbeddingConfig{
			Defaults: []common.ModelConfig{{Provider: "openai", Model: "text-embedding-ada-002"}},
		},
	}
	db.PersistWorkspace(context.Background(), workspace1)
	db.PersistWorkspaceConfig(context.Background(), workspace1.Id, config1)

	workspace2 := models.Workspace{Id: "workspace2", Name: "Workspace Two", LocalRepoDir: "/path/to/repo2"}
	db.PersistWorkspace(context.Background(), workspace2)

	testCases := []struct {
		name           string
		workspaceId    string
		expectedStatus int
		expectedBody   WorkspaceResponse
	}{
		{
			name:           "returns workspace with config correctly",
			workspaceId:    "workspace1",
			expectedStatus: http.StatusOK,
			expectedBody: WorkspaceResponse{
				Id:              "workspace1",
				Created:         workspace1.Created,
				Updated:         workspace1.Updated,
				Name:            "Workspace One",
				LocalRepoDir:    "/path/to/repo1",
				LLMConfig:       config1.LLM,
				EmbeddingConfig: config1.Embedding,
			},
		},
		{
			name:           "returns 404 when workspace does not exist",
			workspaceId:    "nonexistent",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "returns workspace without config when config does not exist",
			workspaceId:    "workspace2",
			expectedStatus: http.StatusOK,
			expectedBody: WorkspaceResponse{
				Id:           "workspace2",
				Created:      workspace2.Created,
				Updated:      workspace2.Updated,
				Name:         "Workspace Two",
				LocalRepoDir: "/path/to/repo2",
				LLMConfig: common.LLMConfig{
					Defaults:       []common.ModelConfig{},
					UseCaseConfigs: make(map[string][]common.ModelConfig),
				},
				EmbeddingConfig: common.EmbeddingConfig{
					Defaults:       []common.ModelConfig{},
					UseCaseConfigs: make(map[string][]common.ModelConfig),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(resp)
			c.Request = httptest.NewRequest("GET", "/v1/workspaces/"+tc.workspaceId, nil)
			c.Params = gin.Params{{Key: "workspaceId", Value: tc.workspaceId}}

			ctrl.GetWorkspaceByIdHandler(c)

			assert.Equal(t, tc.expectedStatus, resp.Code)

			if tc.expectedStatus == http.StatusOK {
				var result struct {
					Workspace WorkspaceResponse `json:"workspace"`
				}
				err := json.Unmarshal(resp.Body.Bytes(), &result)
				assert.NoError(t, err)

				actualWorkspace := result.Workspace

				assert.Equal(t, tc.expectedBody.Id, actualWorkspace.Id)
				assert.Equal(t, tc.expectedBody.Name, actualWorkspace.Name)
				assert.Equal(t, tc.expectedBody.LocalRepoDir, actualWorkspace.LocalRepoDir)
				assert.Equal(t, tc.expectedBody.Created, actualWorkspace.Created)
				assert.Equal(t, tc.expectedBody.Updated, actualWorkspace.Updated)

				if tc.workspaceId == "workspace1" {
					assert.Equal(t, tc.expectedBody.LLMConfig, actualWorkspace.LLMConfig)
					assert.Equal(t, tc.expectedBody.EmbeddingConfig, actualWorkspace.EmbeddingConfig)
				} else {
					assert.Empty(t, actualWorkspace.LLMConfig.Defaults)
					assert.Empty(t, actualWorkspace.LLMConfig.UseCaseConfigs)
					assert.Empty(t, actualWorkspace.EmbeddingConfig.Defaults)
					assert.Empty(t, actualWorkspace.EmbeddingConfig.UseCaseConfigs)
				}
			} else {
				var result gin.H
				err := json.Unmarshal(resp.Body.Bytes(), &result)
				assert.NoError(t, err)
				assert.Equal(t, gin.H{"error": "Workspace not found"}, result)
			}
		})
	}
}
