package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/srv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

type DevAgentManagerActivities struct {
	Storage        srv.Storage
	TemporalClient client.Client
}

type TaskUpdate struct {
	Status    domain.TaskStatus
	AgentType domain.AgentType
}

func (ima *DevAgentManagerActivities) UpdateTaskByTaskId(ctx context.Context, workspaceId, taskId string, update TaskUpdate) error {
	return ima.Storage.UpdateTaskStatus(ctx, workspaceId, taskId, update.Status, update.AgentType)
}

func (ima *DevAgentManagerActivities) UpdateTask(ctx context.Context, workspaceId, workflowId string, update TaskUpdate) error {
	// Recursive function to find a workflow record with parent_id that starts with "task_"
	var findWorkflowParentTaskId func(string) (string, error)
	findWorkflowParentTaskId = func(currentWorkflowId string) (string, error) {
		flow, err := ima.Storage.GetFlow(ctx, workspaceId, currentWorkflowId)
		if err != nil {
			return "", fmt.Errorf("Failed to retrieve workflow record for workflowId %s: %v", currentWorkflowId, err)
		}

		if strings.HasPrefix(flow.ParentId, "task_") {
			return flow.ParentId, nil
		} else if strings.HasPrefix(flow.ParentId, "workflow_") {
			return findWorkflowParentTaskId(flow.ParentId)
		}

		return "", fmt.Errorf("No task workflow found for workflowId: %s", workflowId)
	}

	// Find the task id
	taskId, err := findWorkflowParentTaskId(workflowId)
	if err != nil {
		return err
	}

	return ima.Storage.UpdateTaskStatus(ctx, workspaceId, taskId, update.Status, update.AgentType)
}

func (ima *DevAgentManagerActivities) UpdateTaskForUserRequest(ctx context.Context, workspaceId, workflowId string) error {
	// Recursive function to find a workflow record with parent_id that starts with "task_"
	var findWorkflowParentTaskId func(string) (string, error)
	findWorkflowParentTaskId = func(currentWorkflowId string) (string, error) {
		flow, err := ima.Storage.GetFlow(ctx, workspaceId, currentWorkflowId)
		if err != nil {
			return "", fmt.Errorf("Failed to retrieve workflow record for workflowId %s: %v", currentWorkflowId, err)
		}

		if strings.HasPrefix(flow.ParentId, "task_") {
			return flow.ParentId, nil
		} else if strings.HasPrefix(flow.ParentId, "workflow_") {
			return findWorkflowParentTaskId(flow.ParentId)
		}

		return "", fmt.Errorf("No task workflow found for workflowId: %s", workflowId)
	}

	// Find the task id
	taskId, err := findWorkflowParentTaskId(workflowId)
	if err != nil {
		return err
	}

	return ima.Storage.UpdateTaskStatus(ctx, workspaceId, taskId, domain.TaskStatusBlocked, domain.AgentTypeHuman)
}

func (ima *DevAgentManagerActivities) PutWorkflow(ctx context.Context, flow domain.Flow) (err error) {
	err = ima.Storage.PersistFlow(ctx, flow)
	return err
}

func (ima *DevAgentManagerActivities) CompleteFlowParentTask(ctx context.Context, workspaceId, parentId, flowStatus string) (err error) {
	var taskStatus domain.TaskStatus
	switch flowStatus {
	case "completed":
		taskStatus = domain.TaskStatusComplete
	case "canceled":
		taskStatus = domain.TaskStatusCanceled
	case "failed":
		taskStatus = domain.TaskStatusFailed
	default:
		return fmt.Errorf("Unrecognized flow status: '%s'", flowStatus)
	}
	return ima.Storage.UpdateTaskStatus(ctx, workspaceId, parentId, taskStatus, domain.AgentTypeNone)
}

func (ima *DevAgentManagerActivities) PassOnUserResponse(userResponse flow_action.UserResponse) (err error) {
	// Signal both the legacy unscoped channel and the FlowActionId-specific
	// channel so workflows on either side of the version gate receive the
	// response. Each workflow only listens on one of these names, so the
	// unconsumed signal is harmless.
	signalNames := []string{SignalNameUserResponse}
	if userResponse.FlowActionId != "" {
		signalNames = append(signalNames, flow_action.UserResponseSignalName(userResponse.FlowActionId))
	}
	for _, name := range signalNames {
		err = ima.TemporalClient.SignalWorkflow(context.Background(), userResponse.TargetWorkflowId, "", name, userResponse)
		if err != nil {
			if err.Error() == "workflow execution already completed" {
				log.Warn().Msg("we tried to pass on a user response to a workflow that already completed, something must be wrong")
				return nil
			}
			return err
		}
	}
	return nil
}

func (ima *DevAgentManagerActivities) GetWorkflow(ctx context.Context, workspaceId, workflowId string) (message domain.Flow, err error) {
	log := activity.GetLogger(ctx)
	flow, err := ima.Storage.GetFlow(ctx, workspaceId, workflowId)
	if err != nil {
		log.Error("Failed to retrieve workflow record", "Error", err)
		return domain.Flow{}, err
	}
	return flow, nil
}

func (ima *DevAgentManagerActivities) CreatePendingUserRequest(ctx context.Context, workspaceId string, req flow_action.RequestForUser) error {
	if req.FlowActionId == "" {
		flowAction := domain.FlowAction{
			WorkspaceId:      workspaceId,
			Id:               "fa_" + ksuid.New().String(),
			FlowId:           req.OriginWorkflowId,
			Created:          time.Now().UTC(),
			Updated:          time.Now().UTC(),
			SubflowId:        req.SubflowId,
			SubflowName:      req.Subflow,
			ActionType:       "user_request",
			ActionParams:     req.ActionParams(),
			ActionStatus:     domain.ActionStatusPending,
			IsHumanAction:    true,
			IsCallbackAction: true,
		}

		err := ima.Storage.PersistFlowAction(ctx, flowAction)
		if err != nil {
			return fmt.Errorf("Failed to persist flow action: %v", err)
		}
	} else {
		_, err := ima.Storage.GetFlowAction(ctx, workspaceId, req.FlowActionId)
		if err != nil {
			if err == srv.ErrNotFound {
				return fmt.Errorf("Flow action with id %s not found in workspace %s", req.FlowActionId, workspaceId)
			}
			return fmt.Errorf("Failed to find existing flow action: %v", err)
		}
	}

	return nil
}

type ListWorkspacesResult struct {
	WorkspaceIds []string `json:"workspaceIds"`
}

func (ima *DevAgentManagerActivities) ListWorkspaces(ctx context.Context) (ListWorkspacesResult, error) {
	workspaces, err := ima.Storage.GetAllWorkspaces(ctx)
	if err != nil {
		return ListWorkspacesResult{}, fmt.Errorf("failed to list workspaces: %w", err)
	}
	ids := make([]string, len(workspaces))
	for i, ws := range workspaces {
		ids[i] = ws.Id
	}
	return ListWorkspacesResult{WorkspaceIds: ids}, nil
}

func (ima *DevAgentManagerActivities) FindWorkspaceById(ctx context.Context, workspaceId string) (domain.Workspace, error) {
	log := activity.GetLogger(ctx)
	workspace, err := ima.Storage.GetWorkspace(ctx, workspaceId)
	if err != nil {
		log.Error("Failed to retrieve workspace record", "Error", err)
		return domain.Workspace{}, err
	}
	return workspace, nil
}

type StaleWorktreeCandidate struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Warning string `json:"warning,omitempty"`
}

type CleanupStaleWorktreesReport struct {
	WorkspaceId string                   `json:"workspaceId"`
	BaseDir     string                   `json:"baseDir"`
	DryRun      bool                     `json:"dryRun"`
	Candidates  []StaleWorktreeCandidate `json:"candidates"`
	Protected   []StaleWorktreeCandidate `json:"protected"`
}

func (ima *DevAgentManagerActivities) CleanupStaleWorktrees(ctx context.Context, input CleanupStaleWorktreesInput) (CleanupStaleWorktreesReport, error) {
	infoLog := func(msg string, kv ...any) {
		if activity.IsActivity(ctx) {
			activity.GetLogger(ctx).Info(msg, kv...)
			return
		}

		ev := log.Info()
		for i := 0; i+1 < len(kv); i += 2 {
			key, ok := kv[i].(string)
			if !ok || strings.TrimSpace(key) == "" {
				continue
			}
			ev = ev.Interface(key, kv[i+1])
		}
		ev.Msg(msg)
	}

	errorLog := func(msg string, kv ...any) {
		if activity.IsActivity(ctx) {
			activity.GetLogger(ctx).Error(msg, kv...)
			return
		}

		ev := log.Error()
		for i := 0; i+1 < len(kv); i += 2 {
			key, ok := kv[i].(string)
			if !ok || strings.TrimSpace(key) == "" {
				continue
			}
			ev = ev.Interface(key, kv[i+1])
		}
		ev.Msg(msg)
	}

	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return CleanupStaleWorktreesReport{}, err
	}
	baseDir := filepath.Join(sidekickDataHome, "worktrees", input.WorkspaceId)

	report := CleanupStaleWorktreesReport{
		WorkspaceId: input.WorkspaceId,
		BaseDir:     baseDir,
		DryRun:      input.DryRun,
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return CleanupStaleWorktreesReport{}, fmt.Errorf("failed to read worktrees directory: %w", err)
	}

	worktrees, err := ima.Storage.GetWorktrees(ctx, input.WorkspaceId)
	if err != nil {
		return CleanupStaleWorktreesReport{}, err
	}

	protected := make(map[string]StaleWorktreeCandidate)
	inactiveReasons := make(map[string]string)
	inactiveWarnings := make(map[string]string)

	for _, wt := range worktrees {
		workingDir := strings.TrimSpace(wt.WorkingDirectory)
		if workingDir == "" {
			continue
		}

		active, reason, warning, err := ima.isWorktreeActive(ctx, input.WorkspaceId, wt)
		if err != nil {
			errorLog("Failed to evaluate worktree activity; treating as protected", "worktreeId", wt.Id, "worktreeDir", workingDir, "error", err)
			protected[workingDir] = StaleWorktreeCandidate{
				Path:    workingDir,
				Reason:  "failed to evaluate worktree activity",
				Warning: "",
			}
			continue
		}

		if active {
			protected[workingDir] = StaleWorktreeCandidate{
				Path:    workingDir,
				Reason:  reason,
				Warning: warning,
			}
			continue
		}

		if strings.TrimSpace(reason) != "" {
			inactiveReasons[workingDir] = reason
		}
		if strings.TrimSpace(warning) != "" {
			inactiveWarnings[workingDir] = warning
		}
	}

	var toDelete []StaleWorktreeCandidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(baseDir, entry.Name())
		if _, ok := protected[dirPath]; ok {
			continue
		}

		reason := inactiveReasons[dirPath]
		if strings.TrimSpace(reason) == "" {
			reason = "not tied to an active flow/task"
		}
		warning := inactiveWarnings[dirPath]

		candidate := StaleWorktreeCandidate{
			Path:    dirPath,
			Reason:  reason,
			Warning: warning,
		}
		report.Candidates = append(report.Candidates, candidate)

		if input.DryRun {
			infoLog("Stale worktree candidate (dry-run)", "path", dirPath, "reason", reason, "warning", warning)
			continue
		}

		toDelete = append(toDelete, candidate)
	}

	if !input.DryRun && len(toDelete) > 0 {
		deleteOne := func(candidate StaleWorktreeCandidate) {
			dirPath := candidate.Path
			reason := candidate.Reason
			warning := candidate.Warning

			safeRemoveAll := func() error {
				cleanBase := filepath.Clean(baseDir)
				cleanPath := filepath.Clean(dirPath)
				if cleanPath != cleanBase && !strings.HasPrefix(cleanPath, cleanBase+string(os.PathSeparator)) {
					return fmt.Errorf("refusing to delete path outside baseDir: %s", cleanPath)
				}
				return os.RemoveAll(dirPath)
			}

			commonGitDirCmd := exec.CommandContext(ctx, "git", "-C", dirPath, "rev-parse", "--git-common-dir")
			commonGitDirOut, err := commonGitDirCmd.CombinedOutput()
			if err != nil {
				if rmErr := safeRemoveAll(); rmErr != nil {
					errorLog(
						"Failed to locate git common dir for worktree; and direct delete failed",
						"path", dirPath,
						"error", err,
						"output", strings.TrimSpace(string(commonGitDirOut)),
						"removeError", rmErr,
					)
					return
				}
				infoLog("Deleted stale worktree directory (direct delete fallback)", "path", dirPath, "reason", reason, "warning", warning)
				return
			}

			commonGitDir := strings.TrimSpace(string(commonGitDirOut))
			if commonGitDir == "" {
				if rmErr := safeRemoveAll(); rmErr != nil {
					errorLog("Failed to locate git common dir for worktree; and direct delete failed", "path", dirPath, "error", "empty git common dir", "removeError", rmErr)
					return
				}
				infoLog("Deleted stale worktree directory (direct delete fallback)", "path", dirPath, "reason", reason, "warning", warning)
				return
			}
			if !filepath.IsAbs(commonGitDir) {
				commonGitDir = filepath.Join(dirPath, commonGitDir)
			}

			// Delete the branch before removing the worktree to avoid
			// leaving orphaned branches behind.
			branchCmd := exec.CommandContext(ctx, "git", "-C", dirPath, "rev-parse", "--abbrev-ref", "HEAD")
			branchOut, branchErr := branchCmd.CombinedOutput()
			branchName := strings.TrimSpace(string(branchOut))
			if branchErr == nil && branchName != "" && branchName != "HEAD" {
				// Detach HEAD so the branch is not checked out
				shaCmd := exec.CommandContext(ctx, "git", "-C", dirPath, "rev-parse", "HEAD")
				shaOut, shaErr := shaCmd.CombinedOutput()
				if shaErr == nil {
					sha := strings.TrimSpace(string(shaOut))
					detachCmd := exec.CommandContext(ctx, "git", "-C", dirPath, "checkout", sha)
					if detachOut, detachErr := detachCmd.CombinedOutput(); detachErr != nil {
						errorLog("Failed to detach HEAD before branch delete", "path", dirPath, "branch", branchName, "error", detachErr, "output", strings.TrimSpace(string(detachOut)))
					} else {
						delCmd := exec.CommandContext(ctx, "git", "--git-dir", commonGitDir, "branch", "-D", branchName)
						if delOut, delErr := delCmd.CombinedOutput(); delErr != nil {
							errorLog("Failed to delete branch for stale worktree", "path", dirPath, "branch", branchName, "error", delErr, "output", strings.TrimSpace(string(delOut)))
						} else {
							infoLog("Deleted branch for stale worktree", "path", dirPath, "branch", branchName)
						}
					}
				}
			}

			// Sidekick worktrees are created locked, so removal requires --force twice.
			removeCmd := exec.CommandContext(ctx, "git", "--git-dir", commonGitDir, "worktree", "remove", dirPath, "--force", "--force")
			removeOut, err := removeCmd.CombinedOutput()
			if err != nil {
				fallbackCmd := exec.CommandContext(ctx, "git", "-C", dirPath, "worktree", "remove", ".", "--force", "--force")
				fallbackOut, fallbackErr := fallbackCmd.CombinedOutput()
				if fallbackErr != nil {
					if rmErr := safeRemoveAll(); rmErr != nil {
						errorLog(
							"Failed to delete stale worktree via git worktree remove; and direct delete failed",
							"path", dirPath,
							"error", err,
							"output", strings.TrimSpace(string(removeOut)),
							"fallbackError", fallbackErr,
							"fallbackOutput", strings.TrimSpace(string(fallbackOut)),
							"removeError", rmErr,
						)
						return
					}
					infoLog("Deleted stale worktree directory (direct delete fallback)", "path", dirPath, "reason", reason, "warning", warning)
					return
				}
			}

			infoLog("Deleted stale worktree directory", "path", dirPath, "reason", reason, "warning", warning)
		}

		maxParallel := runtime.GOMAXPROCS(0)
		if maxParallel < 2 {
			maxParallel = 2
		}
		if maxParallel > 8 {
			maxParallel = 8
		}

		sema := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for _, candidate := range toDelete {
			wg.Add(1)
			go func(candidate StaleWorktreeCandidate) {
				defer wg.Done()
				sema <- struct{}{}
				defer func() { <-sema }()
				deleteOne(candidate)
			}(candidate)
		}
		wg.Wait()
	}

	for _, entry := range protected {
		report.Protected = append(report.Protected, entry)
	}

	return report, nil
}

func (ima *DevAgentManagerActivities) isWorktreeActive(ctx context.Context, workspaceId string, wt domain.Worktree) (bool, string, string, error) {
	if strings.TrimSpace(wt.FlowId) == "" {
		return true, "no flowId on worktree record", "", nil
	}

	flow, err := ima.Storage.GetFlow(ctx, workspaceId, wt.FlowId)
	if err != nil {
		if err == srv.ErrNotFound {
			return false, "flow not found", "", nil
		}
		return false, "", "", err
	}

	flowFinished := false
	switch flow.Status {
	case "completed", "failed", "canceled":
		flowFinished = true
	}

	if strings.HasPrefix(flow.ParentId, "task_") {
		task, err := ima.Storage.GetTask(ctx, workspaceId, flow.ParentId)
		if err != nil {
			if err == srv.ErrNotFound {
				if flowFinished {
					return false, "flow finished", "flow finished but task missing", nil
				}
				return false, "task not found", "", nil
			}
			return false, "", "", err
		}

		taskFinished := false
		switch task.Status {
		case domain.TaskStatusComplete, domain.TaskStatusFailed, domain.TaskStatusCanceled:
			taskFinished = true
		}

		if flowFinished && !taskFinished {
			return true, "task active", "flow finished but task still active", nil
		}

		if !flowFinished && taskFinished {
			return false, "task finished", "flow still active but task finished", nil
		}

		if taskFinished {
			return false, "task finished", "", nil
		}
		return true, "task active", "", nil
	}

	if flowFinished {
		return false, "flow finished", "", nil
	}

	return true, "flow active", "", nil
}

type CleanupStaleWorktreesInput struct {
	WorkspaceId string `json:"workspaceId"`
	DryRun      bool   `json:"dryRun"`
}

// HibernationCandidate represents an active worktree that qualifies for hibernation.
type HibernationCandidate struct {
	FlowId       string `json:"flowId"`
	WorktreePath string `json:"worktreePath"`
	BranchName   string `json:"branchName"`
	Reason       string `json:"reason"`
}

// HibernationCandidatesInput is the input for FindHibernationCandidates.
type HibernationCandidatesInput struct {
	WorkspaceId       string        `json:"workspaceId"`
	InactivityTimeout time.Duration `json:"inactivityTimeout"`
}

// HibernationCandidatesOutput is the output of FindHibernationCandidates.
type HibernationCandidatesOutput struct {
	Candidates []HibernationCandidate `json:"candidates"`
}

// FindHibernationCandidates returns worktrees tied to active flows whose tasks
// are idle (blocked or in_review) and haven't been updated within the inactivity
// timeout. Task status is the idle signal, which works reliably across local and
// remote environments without filesystem access.
//
// The scope is intentionally limited to flows waiting on user input (blocked or
// in_review) rather than any long-idle flow. Hibernation is delivered as a signal
// that cannot safely interrupt a flow actively using its worktree, so we only
// target flows that are already parked: these have a deterministic resume point
// (the user response, or the next worktree-dependent step) where the worktree
// auto-wakes and the hibernation global state is reconciled. A blocked/in_review
// task untouched for the timeout is precisely the "not touched in a long while"
// worktree we want to reclaim disk from.
func (ima *DevAgentManagerActivities) FindHibernationCandidates(ctx context.Context, input HibernationCandidatesInput) (HibernationCandidatesOutput, error) {
	worktrees, err := ima.Storage.GetWorktrees(ctx, input.WorkspaceId)
	if err != nil {
		return HibernationCandidatesOutput{}, err
	}

	var candidates []HibernationCandidate
	for _, wt := range worktrees {
		workingDir := strings.TrimSpace(wt.WorkingDirectory)
		if workingDir == "" || strings.TrimSpace(wt.FlowId) == "" {
			continue
		}

		flow, err := ima.Storage.GetFlow(ctx, input.WorkspaceId, wt.FlowId)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(flow.ParentId, "task_") {
			continue
		}

		task, err := ima.Storage.GetTask(ctx, input.WorkspaceId, flow.ParentId)
		if err != nil {
			continue
		}

		// Only hibernate worktrees whose tasks are waiting on user action
		if task.Status != domain.TaskStatusBlocked && task.Status != domain.TaskStatusInReview {
			continue
		}

		idleDuration := time.Since(task.Updated)
		if idleDuration < input.InactivityTimeout {
			continue
		}

		candidates = append(candidates, HibernationCandidate{
			FlowId:       wt.FlowId,
			WorktreePath: workingDir,
			BranchName:   wt.Name,
			Reason:       fmt.Sprintf("task %s in %s status, idle for %s", task.Id, task.Status, idleDuration.Round(time.Minute)),
		})
	}

	return HibernationCandidatesOutput{Candidates: candidates}, nil
}
