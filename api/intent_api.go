package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sidekick/dev"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.temporal.io/api/serviceerror"
)

// IntentFileEntry describes a single node in a flow worktree's intent filetree.
type IntentFileEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// WriteIntentFileRequest is the body for saving an intent file's contents.
type WriteIntentFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// StartIntentSubtaskRequest is the body for launching an intent sub-task.
type StartIntentSubtaskRequest struct {
	Update bool `json:"update"`
}

// flowWorktreeDir resolves the working directory of the worktree backing a flow,
// returning an HTTP status code and error suitable for direct responses.
func (ctrl *Controller) flowWorktreeDir(ctx context.Context, workspaceId, flowId string) (string, int, error) {
	if _, err := ctrl.service.GetFlow(ctx, workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return "", http.StatusNotFound, fmt.Errorf("flow not found")
		}
		return "", http.StatusInternalServerError, err
	}

	worktrees, err := ctrl.service.GetWorktreesForFlow(ctx, workspaceId, flowId)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if len(worktrees) == 0 {
		return "", http.StatusNotFound, fmt.Errorf("no worktree found for flow")
	}
	return worktrees[0].WorkingDirectory, 0, nil
}

// resolveWorktreeFilePath joins a worktree-relative path to its worktree dir,
// rejecting empty, absolute, or traversal paths that would escape the worktree.
func resolveWorktreeFilePath(worktreeDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("path is required")
	}
	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be relative")
	}
	full := filepath.Join(worktreeDir, cleaned)
	rel, err := filepath.Rel(worktreeDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes worktree")
	}
	return full, nil
}

// listIntentFiles walks the worktree's intent/ directory, returning entries with
// worktree-relative slash paths. A missing intent/ directory yields no entries.
func listIntentFiles(worktreeDir string) ([]IntentFileEntry, error) {
	intentDir := filepath.Join(worktreeDir, "intent")
	info, err := os.Stat(intentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []IntentFileEntry{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return []IntentFileEntry{}, nil
	}

	entries := []IntentFileEntry{}
	err = filepath.WalkDir(intentDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == intentDir {
			return nil
		}
		rel, err := filepath.Rel(worktreeDir, path)
		if err != nil {
			return err
		}
		entries = append(entries, IntentFileEntry{
			Path:  filepath.ToSlash(rel),
			IsDir: d.IsDir(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// ListIntentFilesHandler returns the intent filetree for a flow's worktree.
func (ctrl *Controller) ListIntentFilesHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	worktreeDir, status, err := ctrl.flowWorktreeDir(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	entries, err := listIntentFiles(worktreeDir)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to list intent files")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list intent files"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": entries})
}

// ReadIntentFileHandler returns the contents of a single file within a flow's worktree.
func (ctrl *Controller) ReadIntentFileHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	worktreeDir, status, err := ctrl.flowWorktreeDir(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	fullPath, err := resolveWorktreeFilePath(worktreeDir, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
			return
		}
		log.Error().Err(err).Str("path", fullPath).Msg("Failed to read intent file")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"path": c.Query("path"), "content": string(content)})
}

// WriteIntentFileHandler saves the contents of a file within a flow's worktree,
// creating any missing parent directories. It is the autosave target for the canvas.
func (ctrl *Controller) WriteIntentFileHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	var req WriteIntentFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	worktreeDir, status, err := ctrl.flowWorktreeDir(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	fullPath, err := resolveWorktreeFilePath(worktreeDir, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		log.Error().Err(err).Str("path", fullPath).Msg("Failed to create intent file directory")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}
	if err := os.WriteFile(fullPath, []byte(req.Content), 0o644); err != nil {
		log.Error().Err(err).Str("path", fullPath).Msg("Failed to write intent file")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"path": req.Path})
}

// StartIntentSubtaskHandler signals the IddWorkflow to commit the current intent
// state and spawn a sub-task implementing it.
func (ctrl *Controller) StartIntentSubtaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	var req StartIntentSubtaskRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
	}

	if _, err := ctrl.service.GetFlow(c.Request.Context(), workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	err := ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNameStartIntentSubtask, dev.StartIntentSubtaskSignal{Update: req.Update})
	if err != nil {
		var serviceErrNotFound *serviceerror.NotFound
		if errors.As(err, &serviceErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Flow with ID %s not found", flowId)})
			return
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to signal intent sub-task start")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start intent sub-task: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Intent sub-task started"})
}
