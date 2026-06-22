package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupIntentTestFlow persists a workspace, flow, and a worktree pointing at
// worktreeDir, returning the workspace and flow ids for intent API requests.
func setupIntentTestFlow(t *testing.T, ctrl Controller, worktreeDir string) (string, string) {
	t.Helper()
	ctx := context.Background()
	workspaceId := "ws_" + ksuid.New().String()
	flowId := "flow_" + ksuid.New().String()

	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: workspaceId}))
	require.NoError(t, ctrl.service.PersistFlow(ctx, domain.Flow{Id: flowId, WorkspaceId: workspaceId}))
	require.NoError(t, ctrl.service.PersistWorktree(ctx, domain.Worktree{
		Id:               "wt_" + ksuid.New().String(),
		FlowId:           flowId,
		WorkspaceId:      workspaceId,
		Name:             "side/intent-test",
		Created:          time.Now(),
		WorkingDirectory: worktreeDir,
	}))

	return workspaceId, flowId
}

func TestListIntentFilesHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	worktreeDir := t.TempDir()
	workspaceId, flowId := setupIntentTestFlow(t, ctrl, worktreeDir)

	listURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/files", workspaceId, flowId)

	// No intent directory yet: empty filetree.
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var emptyResp struct {
		Files []IntentFileEntry `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &emptyResp))
	assert.Empty(t, emptyResp.Files)

	// Create some intent files, including a generated one in a subdirectory.
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeDir, "intent", ".generated"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "intent", "mission.md"), []byte("# Mission"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "intent", ".generated", "inferred.md"), []byte("inferred"), 0o644))

	req, _ = http.NewRequest(http.MethodGet, listURL, nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Files []IntentFileEntry `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	paths := map[string]bool{}
	for _, f := range resp.Files {
		paths[f.Path] = f.IsDir
	}
	assert.Contains(t, paths, "intent/mission.md")
	assert.False(t, paths["intent/mission.md"])
	assert.Contains(t, paths, "intent/.generated")
	assert.True(t, paths["intent/.generated"])
	assert.Contains(t, paths, "intent/.generated/inferred.md")
}

func TestWriteAndReadIntentFileHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	worktreeDir := t.TempDir()
	workspaceId, flowId := setupIntentTestFlow(t, ctrl, worktreeDir)

	writeURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/file", workspaceId, flowId)
	body, _ := json.Marshal(WriteIntentFileRequest{Path: "intent/requirements.md", Content: "# Requirements\n"})
	req, _ := http.NewRequest(http.MethodPut, writeURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	saved, err := os.ReadFile(filepath.Join(worktreeDir, "intent", "requirements.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Requirements\n", string(saved))

	readURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/file?path=intent/requirements.md", workspaceId, flowId)
	req, _ = http.NewRequest(http.MethodGet, readURL, nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "intent/requirements.md", resp.Path)
	assert.Equal(t, "# Requirements\n", resp.Content)
}

func TestReadIntentFileHandler_Errors(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	worktreeDir := t.TempDir()
	workspaceId, flowId := setupIntentTestFlow(t, ctrl, worktreeDir)

	base := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/file", workspaceId, flowId)

	t.Run("missing file", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"?path=intent/missing.md", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"?path=../../etc/passwd", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing path", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestIntentHandlers_FlowNotFound(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	listURL := "/api/v1/workspaces/ws-missing/flows/flow-missing/intent/files"
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestIntentHandlers_NoWorktree(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	ctx := context.Background()
	workspaceId := "ws_" + ksuid.New().String()
	flowId := "flow_" + ksuid.New().String()
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: workspaceId}))
	require.NoError(t, ctrl.service.PersistFlow(ctx, domain.Flow{Id: flowId, WorkspaceId: workspaceId}))

	listURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/files", workspaceId, flowId)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestStartIntentSubtaskHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	workspaceId, flowId := setupIntentTestFlow(t, ctrl, t.TempDir())

	startURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/start_subtask", workspaceId, flowId)

	t.Run("with update body", func(t *testing.T) {
		body, _ := json.Marshal(StartIntentSubtaskRequest{Update: true})
		req, _ := http.NewRequest(http.MethodPost, startURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("with empty body", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, startURL, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestStartIntentSubtaskHandler_FlowNotFound(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	startURL := "/api/v1/workspaces/ws-missing/flows/flow-missing/intent/start_subtask"
	req, _ := http.NewRequest(http.MethodPost, startURL, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
