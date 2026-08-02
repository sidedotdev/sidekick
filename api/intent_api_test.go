package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"sidekick/dev"
	"sidekick/domain"
	"sidekick/mocks"

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

func TestReadIntentFileHandler_CommittedContent(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	worktreeDir := t.TempDir()
	workspaceId, flowId := setupIntentTestFlow(t, ctrl, worktreeDir)

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = worktreeDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}

	runGit("init", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeDir, "intent"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "intent", "mission.md"), []byte("# Mission\nv1\n"), 0o644))
	runGit("add", ".")
	runGit("commit", "-m", "initial")

	// Modify the working copy after the commit.
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "intent", "mission.md"), []byte("# Mission\nv1 updated\n"), 0o644))
	// Add a brand-new untracked file too.
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "intent", "new.md"), []byte("brand new\n"), 0o644))

	read := func(relPath string) (string, string) {
		t.Helper()
		readURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/file?path=%s", workspaceId, flowId, relPath)
		req, _ := http.NewRequest(http.MethodGet, readURL, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		var resp struct {
			Path             string `json:"path"`
			Content          string `json:"content"`
			CommittedContent string `json:"committedContent"`
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		return resp.Content, resp.CommittedContent
	}

	content, committed := read("intent/mission.md")
	assert.Equal(t, "# Mission\nv1 updated\n", content)
	assert.Equal(t, "# Mission\nv1\n", committed)

	content, committed = read("intent/new.md")
	assert.Equal(t, "brand new\n", content)
	assert.Equal(t, "", committed)
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

func TestIntentHandlers_WaitsForWorktree(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	ctx := context.Background()
	workspaceId := "ws_" + ksuid.New().String()
	flowId := "flow_" + ksuid.New().String()
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: workspaceId}))
	require.NoError(t, ctrl.service.PersistFlow(ctx, domain.Flow{Id: flowId, WorkspaceId: workspaceId}))

	worktreeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeDir, "intent"), 0o755))

	prevTimeout := flowWorktreeWaitTimeout
	prevInterval := flowWorktreePollInterval
	// Generous deadline: the handler returns as soon as the worktree appears
	// (~100ms), but a loaded machine running the full suite can be slow.
	flowWorktreeWaitTimeout = 10 * time.Second
	flowWorktreePollInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		flowWorktreeWaitTimeout = prevTimeout
		flowWorktreePollInterval = prevInterval
	})

	// Persist the worktree after a short delay to simulate the IDD workflow
	// creating it just after the canvas issues its first request.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = ctrl.service.PersistWorktree(context.Background(), domain.Worktree{
			Id:               "wt_" + ksuid.New().String(),
			FlowId:           flowId,
			WorkspaceId:      workspaceId,
			Name:             "side/intent-test",
			Created:          time.Now(),
			WorkingDirectory: worktreeDir,
		})
	}()

	listURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/files", workspaceId, flowId)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestIntentHandlers_NoWorktree(t *testing.T) {
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	ctx := context.Background()
	workspaceId := "ws_" + ksuid.New().String()
	flowId := "flow_" + ksuid.New().String()
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: workspaceId}))
	require.NoError(t, ctrl.service.PersistFlow(ctx, domain.Flow{Id: flowId, WorkspaceId: workspaceId}))

	prevTimeout := flowWorktreeWaitTimeout
	prevInterval := flowWorktreePollInterval
	flowWorktreeWaitTimeout = 50 * time.Millisecond
	flowWorktreePollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		flowWorktreeWaitTimeout = prevTimeout
		flowWorktreePollInterval = prevInterval
	})

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
	mockTemporalClient := ctrl.temporalClient.(*mocks.Client)

	startURL := fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/intent/start_subtask", workspaceId, flowId)

	lastSubtaskSignal := func(t *testing.T) dev.StartIntentSubtaskSignal {
		t.Helper()
		for i := len(mockTemporalClient.Calls) - 1; i >= 0; i-- {
			call := mockTemporalClient.Calls[i]
			if call.Method != "SignalWorkflow" {
				continue
			}
			sig, ok := call.Arguments.Get(4).(dev.StartIntentSubtaskSignal)
			require.True(t, ok, "SignalWorkflow payload should be a StartIntentSubtaskSignal")
			return sig
		}
		require.FailNow(t, "no SignalWorkflow call recorded")
		return dev.StartIntentSubtaskSignal{}
	}

	t.Run("with update body", func(t *testing.T) {
		body, _ := json.Marshal(StartIntentSubtaskRequest{Update: true})
		req, _ := http.NewRequest(http.MethodPost, startURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		sig := lastSubtaskSignal(t)
		assert.True(t, sig.Update)
		assert.True(t, sig.Planned, "manually created sub-tasks must run as planned dev")
	})

	t.Run("with empty body", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, startURL, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		sig := lastSubtaskSignal(t)
		assert.False(t, sig.Update)
		assert.True(t, sig.Planned, "manually created sub-tasks must run as planned dev")
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
