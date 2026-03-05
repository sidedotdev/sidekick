package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/mocks"
	"sidekick/srv/sqlite"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDevAgentManagerActivities(t *testing.T) *DevAgentManagerActivities {
	return &DevAgentManagerActivities{
		Storage:        sqlite.NewTestSqliteStorage(t, "dev_agent_test"),
		TemporalClient: &mocks.Client{},
	}
}

func TestUpdateTaskForUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities(t)
	storage := ima.Storage

	workspaceId := "testWorkspace"
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_testTask",
	}
	flow := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "workflow_testWorkflow",
		ParentId:    task.Id,
	}
	err := storage.PersistTask(context.Background(), task)
	assert.Nil(t, err)

	err = storage.PersistFlow(context.Background(), flow)
	assert.Nil(t, err)

	err = ima.UpdateTaskForUserRequest(context.Background(), workspaceId, flow.Id)
	assert.Nil(t, err)

	// Retrieve the task from the database
	updatedTask, err := storage.GetTask(context.Background(), workspaceId, task.Id)
	assert.Nil(t, err)

	// Check that the task was updated appropriately
	assert.Equal(t, domain.AgentTypeHuman, updatedTask.AgentType)
	assert.Equal(t, domain.TaskStatusBlocked, updatedTask.Status)
	assert.False(t, updatedTask.Updated.IsZero(), "Updated time should be set")
}

func TestUpdateTask(t *testing.T) {
	ima := newDevAgentManagerActivities(t)
	storage := ima.Storage

	workspaceId := "testWorkspace"
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_updateTaskTest",
		Status:      domain.TaskStatusToDo,
		AgentType:   domain.AgentTypeLLM,
	}
	flow := domain.Flow{
		WorkspaceId: workspaceId,
		Id:          "workflow_updateTaskTest",
		ParentId:    task.Id,
	}
	err := storage.PersistTask(context.Background(), task)
	assert.Nil(t, err)

	err = storage.PersistFlow(context.Background(), flow)
	assert.Nil(t, err)

	update := TaskUpdate{
		Status:    domain.TaskStatusInReview,
		AgentType: domain.AgentTypeHuman,
	}

	err = ima.UpdateTask(context.Background(), workspaceId, flow.Id, update)
	assert.Nil(t, err)

	// Retrieve the task from the database
	updatedTask, err := storage.GetTask(context.Background(), workspaceId, task.Id)
	assert.Nil(t, err)

	// Check that the task was updated appropriately
	assert.Equal(t, update.AgentType, updatedTask.AgentType)
	assert.Equal(t, update.Status, updatedTask.Status)
	assert.False(t, updatedTask.Updated.IsZero(), "Updated time should be set")
}

func TestCreatePendingUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities(t)
	storage := ima.Storage
	ctx := context.Background()

	workspaceId := "testWorkspace"
	flowId := "fakeWorkflowId"
	request := flow_action.RequestForUser{
		OriginWorkflowId: flowId,
		Content:          "request content",
		Subflow:          "fakeSubflow",
		RequestKind:      flow_action.RequestKindFreeForm,
	}

	err := ima.CreatePendingUserRequest(ctx, workspaceId, request)
	assert.Nil(t, err)

	flowActions, err := storage.GetFlowActions(context.Background(), workspaceId, flowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)

	flowAction := flowActions[0]

	assert.Equal(t, "user_request", flowAction.ActionType)
	assert.Equal(t, map[string]interface{}{
		"requestKind":    string(request.RequestKind),
		"requestContent": request.Content,
	}, flowAction.ActionParams)
	assert.Equal(t, domain.ActionStatusPending, flowAction.ActionStatus)
	assert.True(t, flowAction.IsHumanAction)
	assert.True(t, flowAction.IsCallbackAction)
	assert.Equal(t, request.Subflow, flowAction.SubflowName)
	assert.False(t, flowAction.Created.IsZero(), "Created time should be set")
	assert.False(t, flowAction.Updated.IsZero(), "Updated time should be set")

	// Retrieve the flow action from the database
	persistedFlowAction, err := storage.GetFlowAction(context.Background(), workspaceId, flowAction.Id)
	assert.Nil(t, err)

	// Check that the flow action was persisted appropriately
	assert.Equal(t, flowAction, persistedFlowAction)
}

func TestExistingUserRequest(t *testing.T) {
	ima := newDevAgentManagerActivities(t)
	storage := ima.Storage
	ctx := context.Background()

	workspaceId := "testWorkspace"
	flowId := "fakeWorkflowId"
	flowActionId := "fakeFlowActionId"
	request := flow_action.RequestForUser{
		OriginWorkflowId: flowId,
		FlowActionId:     flowActionId,
		Content:          "request content",
		Subflow:          "fakeSubflow",
		RequestKind:      flow_action.RequestKindApproval,
	}

	existingFlowAction := domain.FlowAction{
		Id:          flowActionId,
		WorkspaceId: workspaceId,
		FlowId:      flowId,
		ActionType:  "another_action",
		ActionParams: map[string]interface{}{
			"requestContent": request.Content,
			"requestKind":    string(request.RequestKind),
		},
		ActionStatus: domain.ActionStatusStarted,
	}
	err := storage.PersistFlowAction(ctx, existingFlowAction)
	assert.Nil(t, err)

	var flowAction domain.FlowAction
	err = ima.CreatePendingUserRequest(ctx, workspaceId, request)
	assert.Nil(t, err)

	flowActions, err := storage.GetFlowActions(context.Background(), workspaceId, flowId)
	assert.Nil(t, err)
	assert.Len(t, flowActions, 1)

	flowAction = flowActions[0]
	assert.Equal(t, flowAction, existingFlowAction)
	assert.Equal(t, utils.PanicJSON(flowAction), utils.PanicJSON(existingFlowAction))
}

func TestCleanupStaleWorktrees_DeletesBranch(t *testing.T) {
	ima := newDevAgentManagerActivities(t)
	storage := ima.Storage
	ctx := context.Background()

	tmpDir := t.TempDir()
	workspaceId := "ws_branchdelete"

	// Set SIDE_DATA_HOME so CleanupStaleWorktrees resolves our temp directory.
	t.Setenv("SIDE_DATA_HOME", tmpDir)

	worktreesBase := filepath.Join(tmpDir, "worktrees", workspaceId)
	require.NoError(t, os.MkdirAll(worktreesBase, 0o755))

	run := func(name string, args ...string) string {
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %q failed: %s", strings.Join(append([]string{name}, args...), " "), string(out))
		return strings.TrimSpace(string(out))
	}

	// Create a regular repo with an initial commit.
	mainRepo := filepath.Join(tmpDir, "repo")
	run("git", "init", mainRepo)
	run("git", "-C", mainRepo, "config", "user.email", "test@test.com")
	run("git", "-C", mainRepo, "config", "user.name", "Test")
	run("git", "-C", mainRepo, "commit", "--allow-empty", "-m", "init")

	// Add a worktree on a new branch.
	branchName := "sk/stale-branch"
	wtPath := filepath.Join(worktreesBase, "stale-wt")
	run("git", "-C", mainRepo, "worktree", "add", "-b", branchName, wtPath)

	// Verify the branch exists before cleanup.
	mainGitDir := filepath.Join(mainRepo, ".git")
	branchList := run("git", "--git-dir", mainGitDir, "branch", "--list", branchName)
	require.NotEmpty(t, branchList, "branch should exist before cleanup")

	// Persist a finished flow so the worktree is considered stale.
	flowId := "flow_stale1"
	require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
		WorkspaceId: workspaceId,
		Id:          flowId,
		Status:      "completed",
	}))
	require.NoError(t, storage.PersistWorktree(ctx, domain.Worktree{
		Id:               "wt_stale1",
		FlowId:           flowId,
		Name:             branchName,
		WorkspaceId:      workspaceId,
		WorkingDirectory: wtPath,
	}))

	report, err := ima.CleanupStaleWorktrees(ctx, CleanupStaleWorktreesInput{
		WorkspaceId: workspaceId,
		DryRun:      false,
	})
	require.NoError(t, err)
	assert.Len(t, report.Candidates, 1)
	assert.Equal(t, wtPath, report.Candidates[0].Path)

	// The worktree directory should be removed.
	_, statErr := os.Stat(wtPath)
	assert.True(t, os.IsNotExist(statErr), "worktree directory should be removed")

	// The branch should have been deleted from the repository.
	branchListAfter := run("git", "--git-dir", mainGitDir, "branch", "--list", branchName)
	assert.Empty(t, branchListAfter, "branch should be deleted after cleanup")
}
