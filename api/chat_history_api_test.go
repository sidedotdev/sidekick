package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHydrateChatHistoryHandler_HappyPath(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	// Seed KV storage with content blocks
	ctx := context.Background()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello"}
	block2 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "World"}
	block3 := llm2.ContentBlock{
		Type: llm2.ContentBlockTypeToolUse,
		ToolUse: &llm2.ToolUseBlock{
			Id:        "tool-1",
			Name:      "test_tool",
			Arguments: `{"arg": "value"}`,
		},
	}

	block1JSON, _ := json.Marshal(block1)
	block2JSON, _ := json.Marshal(block2)
	block3JSON, _ := json.Marshal(block3)

	err := ctrl.service.MSetRaw(ctx, workspaceId, map[string][]byte{
		flowId + ":msg:block-1": block1JSON,
		flowId + ":msg:block-2": block2JSON,
		flowId + ":msg:block-3": block3JSON,
	})
	require.NoError(t, err)

	// Create request with refs
	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{
			{BlockIds: []string{"block-1"}, Role: "user"},
			{BlockIds: []string{"block-2", "block-3"}, Role: "assistant"},
		},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Messages, 2)

	// First message
	assert.Equal(t, llm2.Role("user"), resp.Messages[0].Role)
	require.Len(t, resp.Messages[0].Content, 1)
	assert.Equal(t, "Hello", resp.Messages[0].Content[0].Text)

	// Second message
	assert.Equal(t, llm2.Role("assistant"), resp.Messages[1].Role)
	require.Len(t, resp.Messages[1].Content, 2)
	assert.Equal(t, "World", resp.Messages[1].Content[0].Text)
	assert.Equal(t, llm2.ContentBlockTypeToolUse, resp.Messages[1].Content[1].Type)
	assert.Equal(t, "test_tool", resp.Messages[1].Content[1].ToolUse.Name)
}

func TestHydrateChatHistoryHandler_MissingBlocksShowError(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	// Seed only one block
	ctx := context.Background()
	block1 := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Exists"}
	block1JSON, _ := json.Marshal(block1)

	err := ctrl.service.MSetRaw(ctx, workspaceId, map[string][]byte{
		flowId + ":msg:block-exists": block1JSON,
	})
	require.NoError(t, err)

	// Request includes a missing block
	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{
			{BlockIds: []string{"block-exists", "block-missing"}, Role: "assistant"},
		},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Messages, 1)
	require.Len(t, resp.Messages[0].Content, 2)
	assert.Equal(t, "Exists", resp.Messages[0].Content[0].Text)
	assert.Contains(t, resp.Messages[0].Content[1].Text, "[hydrate error: missing block block-missing]")
}

func TestHydrateChatHistoryHandler_SingleRef(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	ctx := context.Background()
	block1 := []byte(`{"type":"text","text":"Hello"}`)

	err := ctrl.service.MSetRaw(ctx, workspaceId, map[string][]byte{
		flowId + ":msg:block-1": block1,
	})
	require.NoError(t, err)

	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{
			{BlockIds: []string{"block-1"}, Role: "user"},
		},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Messages, 1)
	require.Len(t, resp.Messages[0].Content, 1)
	assert.Equal(t, "Hello", resp.Messages[0].Content[0].Text)
}

func TestHydrateChatHistoryHandler_EmptyRefs(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Len(t, resp.Messages, 0)
}

func TestHydrateChatHistoryHandler_MalformedBlockShowsError(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	ctx := context.Background()
	// Store invalid data that will fail JSON unmarshal
	err := ctrl.service.MSetRaw(ctx, workspaceId, map[string][]byte{
		flowId + ":msg:block-bad": []byte("not valid json data"),
	})
	require.NoError(t, err)

	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{
			{BlockIds: []string{"block-bad"}, Role: "user"},
		},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Messages, 1)
	require.Len(t, resp.Messages[0].Content, 1)
	assert.Contains(t, resp.Messages[0].Content[0].Text, "[hydrate error:")
	assert.Contains(t, resp.Messages[0].Content[0].Text, "block-bad")
}

func TestHydrateChatHistoryHandler_PreservesOrder(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	workspaceId := "test-workspace"
	flowId := "test-flow"

	ctx := context.Background()
	blocks := map[string][]byte{}
	letters := []string{"A", "B", "C", "D", "E"}
	for i := 1; i <= 5; i++ {
		blocks[flowId+":msg:block-"+string(rune('0'+i))] = []byte(`{"type":"text","text":"` + letters[i-1] + `"}`)
	}

	err := ctrl.service.MSetRaw(ctx, workspaceId, blocks)
	require.NoError(t, err)

	// Request with specific order
	reqBody := HydrateChatHistoryRequest{
		Refs: []llm2.MessageRef{
			{BlockIds: []string{"block-3", "block-1"}, Role: "user"},
			{BlockIds: []string{"block-5", "block-2", "block-4"}, Role: "assistant"},
		},
	}
	reqJSON, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspaceId+"/flows/"+flowId+"/chat_history/hydrate", bytes.NewReader(reqJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HydrateChatHistoryResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Len(t, resp.Messages, 2)

	// First message: block-3 (C), block-1 (A)
	require.Len(t, resp.Messages[0].Content, 2)
	assert.Equal(t, "C", resp.Messages[0].Content[0].Text)
	assert.Equal(t, "A", resp.Messages[0].Content[1].Text)

	// Second message: block-5 (E), block-2 (B), block-4 (D)
	require.Len(t, resp.Messages[1].Content, 3)
	assert.Equal(t, "E", resp.Messages[1].Content[0].Text)
	assert.Equal(t, "B", resp.Messages[1].Content[1].Text)
	assert.Equal(t, "D", resp.Messages[1].Content[2].Text)
}
