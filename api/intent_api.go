package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sidekick/coding/git"
	"sidekick/dev"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.temporal.io/api/serviceerror"
)

// flowWorktreeWaitTimeout bounds how long flowWorktreeDir polls for a
// freshly-created worktree to appear in storage and on disk. It is a package
// variable so tests can shorten it to keep the not-found path fast.
var flowWorktreeWaitTimeout = 10 * time.Second

// flowWorktreePollInterval is the gap between worktree lookup attempts while
// waiting for a new flow's worktree to be persisted and materialized on disk.
var flowWorktreePollInterval = 100 * time.Millisecond

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

// FinishIntentRequest is the body for finishing the idd flow with a merge.
type FinishIntentRequest struct {
	TargetBranch string `json:"targetBranch"`
}

type SetIddAutoModeRequest struct {
	Enabled bool `json:"enabled"`
}

// flowWorktreeDir resolves the working directory of the worktree backing a flow,
// returning an HTTP status code and error suitable for direct responses. A
// freshly-started IDD flow can be navigated to before the worktree row is
// persisted or its directory finishes materializing on disk, so this polls
// until both conditions hold or flowWorktreeWaitTimeout elapses.
func (ctrl *Controller) flowWorktreeDir(ctx context.Context, workspaceId, flowId string) (string, int, error) {
	if _, err := ctrl.service.GetFlow(ctx, workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return "", http.StatusNotFound, fmt.Errorf("flow not found")
		}
		return "", http.StatusInternalServerError, err
	}

	deadline := time.Now().Add(flowWorktreeWaitTimeout)
	for {
		worktrees, err := ctrl.service.GetWorktreesForFlow(ctx, workspaceId, flowId)
		if err != nil {
			return "", http.StatusInternalServerError, err
		}
		if len(worktrees) > 0 {
			dir := worktrees[0].WorkingDirectory
			if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
				return dir, 0, nil
			}
		}

		if time.Now().After(deadline) {
			return "", http.StatusNotFound, fmt.Errorf("no worktree found for flow")
		}
		select {
		case <-ctx.Done():
			return "", http.StatusRequestTimeout, ctx.Err()
		case <-time.After(flowWorktreePollInterval):
		}
	}
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

// committedIntentContent returns the file's content at HEAD as stored in the
// worktree's repository. Returns an empty string (no error) when there's no
// HEAD commit yet or the file isn't tracked, since either case is normal while
// authoring brand-new intent.
func committedIntentContent(ctx context.Context, worktreeDir, relPath string) string {
	cmd := exec.CommandContext(ctx, "git", "show", "HEAD:"+filepath.ToSlash(relPath))
	cmd.Dir = worktreeDir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
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

	committed := committedIntentContent(c.Request.Context(), worktreeDir, c.Query("path"))

	c.JSON(http.StatusOK, gin.H{
		"path":             c.Query("path"),
		"content":          string(content),
		"committedContent": committed,
	})
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

// currentWorktreeBranch returns the branch checked out in the given worktree
// directory, used to identify the idd worktree's source branch.
func currentWorktreeBranch(ctx context.Context, worktreeDir string) string {
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = worktreeDir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// ListIntentBranchesHandler returns the list of local branches in the flow's
// repository alongside the worktree's current branch, for use by the finish-idd
// merge target picker.
func (ctrl *Controller) ListIntentBranchesHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	worktreeDir, status, err := ctrl.flowWorktreeDir(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	branches, err := git.ListLocalBranches(c.Request.Context(), worktreeDir)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to list branches for intent finish")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list branches"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"branches":      branches,
		"currentBranch": currentWorktreeBranch(c.Request.Context(), worktreeDir),
	})
}

// FinishIntentDiffHandler returns the diff that would be merged from the idd
// worktree branch into the given target branch.
func (ctrl *Controller) FinishIntentDiffHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	target := strings.TrimSpace(c.Query("target"))
	if target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target branch is required"})
		return
	}

	worktreeDir, status, err := ctrl.flowWorktreeDir(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	cmd := exec.CommandContext(c.Request.Context(), "git", "diff", target+"...HEAD")
	cmd.Dir = worktreeDir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		log.Error().Err(runErr).Str("workspaceId", workspaceId).Str("flowId", flowId).Str("target", target).Str("stderr", errBuf.String()).Msg("Failed to compute intent finish diff")
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to compute diff against %s", target)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"target": target,
		"diff":   out.String(),
	})
}

// FinishIntentHandler signals the IddWorkflow to merge its worktree into the
// requested target branch and exit.
func (ctrl *Controller) FinishIntentHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	var req FinishIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if strings.TrimSpace(req.TargetBranch) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "targetBranch is required"})
		return
	}

	if _, err := ctrl.service.GetFlow(c.Request.Context(), workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	err := ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNameFinishIdd, dev.FinishIddSignal{TargetBranch: req.TargetBranch})
	if err != nil {
		var serviceErrNotFound *serviceerror.NotFound
		if errors.As(err, &serviceErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Flow with ID %s not found", flowId)})
			return
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to signal intent finish")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to finish intent: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Intent finish requested"})
}

// SetIddAutoModeHandler signals the IddWorkflow to enable or disable the
// background orchestrator's auto sub-task creation.
func (ctrl *Controller) SetIddAutoModeHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	var req SetIddAutoModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if _, err := ctrl.service.GetFlow(c.Request.Context(), workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	err := ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNameSetIddAutoMode, dev.SetIddAutoModeSignal{Enabled: req.Enabled})
	if err != nil {
		var serviceErrNotFound *serviceerror.NotFound
		if errors.As(err, &serviceErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Flow with ID %s not found", flowId)})
			return
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to signal idd auto-mode toggle")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set auto-mode: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Auto-mode updated"})
}

// RunIddOrchestratorHandler asks the IddWorkflow's background orchestrator to
// evaluate current intent state and, if auto-mode is on, launch a sub-task.
// The frontend posts here after its edit-idle heuristic decides a chunk of
// intent is ready, keeping the heuristic out of the workflow.
func (ctrl *Controller) RunIddOrchestratorHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	if _, err := ctrl.service.GetFlow(c.Request.Context(), workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	err := ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNameRunIddOrchestrator, dev.RunIddOrchestratorSignal{})
	if err != nil {
		var serviceErrNotFound *serviceerror.NotFound
		if errors.As(err, &serviceErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Flow with ID %s not found", flowId)})
			return
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to signal idd orchestrator run")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to run orchestrator: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Orchestrator run requested"})
}
