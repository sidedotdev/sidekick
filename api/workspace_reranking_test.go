package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRerankingRoundTrip(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	var workspaceId string

	configs := []common.RerankingConfig{
		{
			Defaults: []common.ModelConfig{{Provider: "company-ranking", Model: "rerank-v3.5"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				"diff_summarize": {{Provider: "cohere", Model: common.DefaultCohereRerankingModel}},
			},
		},
		{Defaults: []common.ModelConfig{}},
		{},
	}
	request := func(method string, config common.RerankingConfig) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"name": "Reranking workspace", "localRepoDir": t.TempDir(),
			"rerankingConfig": config,
		})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "workspaceId", Value: workspaceId}}
		c.Request = httptest.NewRequest(method, "/v1/workspaces/"+workspaceId, bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		switch method {
		case http.MethodPost:
			ctrl.CreateWorkspaceHandler(c)
		case http.MethodPut:
			ctrl.UpdateWorkspaceHandler(c)
		case http.MethodGet:
			ctrl.GetWorkspaceHandler(c)
		}
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var response struct {
			Workspace struct {
				Id              string                 `json:"id"`
				RerankingConfig common.RerankingConfig `json:"rerankingConfig"`
			} `json:"workspace"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		workspaceId = response.Workspace.Id
		assert.Equal(t, config, response.Workspace.RerankingConfig)
		stored, err := ctrl.service.GetWorkspaceConfig(context.Background(), workspaceId)
		require.NoError(t, err)
		assert.Equal(t, config, stored.Reranking)
	}

	for i, config := range configs {
		method := http.MethodPut
		if i == 0 {
			method = http.MethodPost
		}
		request(method, config)
		request(http.MethodGet, config)
	}
}
