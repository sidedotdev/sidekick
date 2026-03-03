package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/srv"
	"strings"
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

	// Update the task record
	task, err := ima.Storage.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return fmt.Errorf("Failed to retrieve task record for taskId %s: %v", taskId, err)
	}

	task.AgentType = update.AgentType
	task.Status = update.Status
	task.Updated = time.Now()

	return ima.Storage.PersistTask(ctx, task)
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

	// Update the task record
	task, err := ima.Storage.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return fmt.Errorf("Failed to retrieve task record for taskId %s: %v", taskId, err)
	}

	task.AgentType = domain.AgentTypeHuman
	task.Status = domain.TaskStatusBlocked
	task.Updated = time.Now()

	return ima.Storage.PersistTask(ctx, task)
}

func (ima *DevAgentManagerActivities) PutWorkflow(ctx context.Context, flow domain.Flow) (err error) {
	err = ima.Storage.PersistFlow(ctx, flow)
	return err
}

func (ima *DevAgentManagerActivities) CompleteFlowParentTask(ctx context.Context, workspaceId, parentId, flowStatus string) (err error) {
	// Retrieve the task using workspaceId and parentId
	task, err := ima.Storage.GetTask(ctx, workspaceId, parentId)
	if err != nil {
		return err
	}

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
	task.Status = taskStatus
	task.AgentType = domain.AgentTypeNone
	task.Updated = time.Now()
	err = ima.Storage.PersistTask(ctx, task)
	if err != nil {
		return err
	}

	return nil
}

func (ima *DevAgentManagerActivities) PassOnUserResponse(userResponse flow_action.UserResponse) (err error) {
	err = ima.TemporalClient.SignalWorkflow(context.Background(), userResponse.TargetWorkflowId, "", SignalNameUserResponse, userResponse)
	if err != nil && err.Error() == "workflow execution already completed" {
		log.Warn().Msg("we tried to pass on a user response to a workflow that already completed, something must be wrong")
		return nil
	}
	return err
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

		report.Candidates = append(report.Candidates, StaleWorktreeCandidate{
			Path:    dirPath,
			Reason:  reason,
			Warning: warning,
		})

		if input.DryRun {
			infoLog("Stale worktree candidate (dry-run)", "path", dirPath, "reason", reason, "warning", warning)
			continue
		}

		if err := os.RemoveAll(dirPath); err != nil {
			errorLog("Failed to delete stale worktree directory", "path", dirPath, "error", err)
			continue
		}
		infoLog("Deleted stale worktree directory", "path", dirPath, "reason", reason, "warning", warning)
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
