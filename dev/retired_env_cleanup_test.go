package dev

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
)

const (
	flowEndCleanupReason = "Sidekick task completed and merged"
	cancelCleanupReason  = "Sidekick task cancelled"
	retiredWorktreeDir   = "/root/worktrees/retired"
)

func retiredModalEnv(dir, sandboxName string, withWorktree bool) retiredEnv {
	entry := retiredEnv{EnvContainer: env.EnvContainer{Env: &env.ModalEnv{
		WorkingDirectory: dir,
		SandboxName:      sandboxName,
		LocalRepoDir:     "/host/repo",
	}}}
	if withWorktree {
		entry.Worktree = &domain.Worktree{Id: "wt_switch", Name: switchTestBranch, WorkingDirectory: dir}
	}
	return entry
}

type retiredEnvCleanupTestInput struct {
	Retired      []retiredEnv
	SwitchTarget *retiredEnv
	Cancel       bool
}

// retiredEnvCleanupTestWorkflow runs a devpod worktree flow that previously
// ran on the retired envs. It either ends normally, cleaning up retired envs
// as the merge path does, or blocks until cancelled.
func retiredEnvCleanupTestWorkflow(ctx workflow.Context, input retiredEnvCleanupTestInput) error {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	state := newFlowEnvState(globalState)
	// Retired envs were each the flow env once, so the env gate would
	// redirect activities on them to the current env unless pinned.
	for _, retired := range input.Retired {
		previous := retired.EnvContainer
		state.publish(&previous)
	}
	container := switchTestSourceEnv()
	state.publish(&container)
	for _, retired := range input.Retired {
		state.retire(retired)
	}
	if input.SwitchTarget != nil {
		target := input.SwitchTarget.EnvContainer
		state.setEnvSwitch(envSwitchState{
			From:           string(env.EnvTypeDevPod),
			To:             string(target.Env.GetType()),
			Target:         &target,
			TargetWorktree: input.SwitchTarget.Worktree,
		})
	}

	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:      env.WithEnvTracker(ctx, state),
			WorkspaceId:  "ws_switch",
			EnvContainer: &container,
			GlobalState:  globalState,
			FlowScope:    &flow_action.FlowScope{},
		},
		Worktree: &domain.Worktree{
			Id:               "wt_switch",
			FlowId:           "flow_switch",
			Name:             switchTestBranch,
			WorkspaceId:      "ws_switch",
			WorkingDirectory: switchTestSourceDir,
		},
	}
	if !input.Cancel {
		cleanupRetiredEnvs(dCtx, dCtx, flowEndCleanupReason)
		return nil
	}
	defer handleFlowCancel(dCtx)
	return workflow.Await(dCtx, func() bool { return false })
}

// retiredEnvCleanupParentWorkflow runs the flow as a child, as flows run in
// production, so that cancellation cleanup can signal its parent.
func retiredEnvCleanupParentWorkflow(ctx workflow.Context, input retiredEnvCleanupTestInput) error {
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WaitForCancellation: true})
	return workflow.ExecuteChildWorkflow(ctx, retiredEnvCleanupTestWorkflow, input).Get(ctx, nil)
}

type cleanedWorktree struct {
	Env    env.EnvIdentity
	Path   string
	Branch string
	Reason string
}

type envCleanupRecorder struct {
	mu        sync.Mutex
	worktrees []cleanedWorktree
	deleted   []env.DeleteSandboxInput
	stopped   []env.StopSandboxInput
}

// mockEnvCleanup records worktree and sandbox cleanup, failing cleanup of any
// worktree path or sandbox name listed in failing.
func mockEnvCleanup(wfEnv *testsuite.TestWorkflowEnvironment, recorder *envCleanupRecorder, failing ...string) {
	cleanupErr := temporal.NewNonRetryableApplicationError("env is gone", "CleanupFailed", nil)
	wfEnv.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, container env.EnvContainer, path, branch, reason string) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.worktrees = append(recorder.worktrees, cleanedWorktree{Env: env.IdentityOf(container), Path: path, Branch: branch, Reason: reason})
			if slices.Contains(failing, path) {
				return cleanupErr
			}
			return nil
		}).Maybe()
	wfEnv.OnActivity(env.DeleteSandboxActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.DeleteSandboxInput) (env.DeleteSandboxOutput, error) {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.deleted = append(recorder.deleted, input)
			if slices.Contains(failing, input.SandboxName) {
				return env.DeleteSandboxOutput{}, cleanupErr
			}
			return env.DeleteSandboxOutput{}, nil
		}).Maybe()
	wfEnv.OnActivity(env.StopSandboxActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.StopSandboxInput) (env.StopSandboxOutput, error) {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.stopped = append(recorder.stopped, input)
			if slices.Contains(failing, input.SandboxName) {
				return env.StopSandboxOutput{}, cleanupErr
			}
			return env.StopSandboxOutput{}, nil
		}).Maybe()
	wfEnv.OnSignalExternalWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

func newEnvGateTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	wfEnv := newEnvSettingsTestEnv(t)
	options := utils.TestWorkerOptions()
	options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor())
	wfEnv.SetWorkerOptions(options)
	return wfEnv
}

func cleanedWorktreeOf(entry retiredEnv, reason string) cleanedWorktree {
	return cleanedWorktree{
		Env:    env.IdentityOf(entry.EnvContainer),
		Path:   entry.EnvContainer.Env.GetWorkingDirectory(),
		Branch: entry.Worktree.Name,
		Reason: reason,
	}
}

func deletedSandboxOf(entry retiredEnv) env.DeleteSandboxInput {
	sandbox := entry.EnvContainer.Env.(env.SandboxEnv)
	return env.DeleteSandboxInput{EnvType: sandbox.GetType(), SandboxName: sandbox.GetSandboxName()}
}

func TestFlowCancelCleansUpRetiredEnvsAndSwitchTarget(t *testing.T) {
	t.Parallel()
	active := retiredEnv{
		EnvContainer: switchTestSourceEnv(),
		Worktree:     &domain.Worktree{Id: "wt_switch", Name: switchTestBranch, WorkingDirectory: switchTestSourceDir},
	}
	retired := retiredModalEnv(retiredWorktreeDir, "side--modal-retired", true)
	target := retiredModalEnv(switchTestTargetDir, "side--modal-target", true)

	tests := []struct {
		name          string
		input         retiredEnvCleanupTestInput
		wantWorktrees []retiredEnv
	}{
		{
			name:          "nothing retired cleans up only the active env",
			input:         retiredEnvCleanupTestInput{},
			wantWorktrees: []retiredEnv{active},
		},
		{
			name:          "retired envs and an in-progress switch target",
			input:         retiredEnvCleanupTestInput{Retired: []retiredEnv{retired}, SwitchTarget: &target},
			wantWorktrees: []retiredEnv{active, retired, target},
		},
		{
			name:          "a switch target that was already retired is cleaned up once",
			input:         retiredEnvCleanupTestInput{Retired: []retiredEnv{target}, SwitchTarget: &target},
			wantWorktrees: []retiredEnv{active, target},
		},
		{
			name:          "a switch target that is already the active env is not cleaned up twice",
			input:         retiredEnvCleanupTestInput{SwitchTarget: &active},
			wantWorktrees: []retiredEnv{active},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvGateTestEnv(t)
			wfEnv.RegisterWorkflow(retiredEnvCleanupTestWorkflow)
			recorder := &envCleanupRecorder{}
			mockEnvCleanup(wfEnv, recorder)
			wfEnv.RegisterDelayedCallback(wfEnv.CancelWorkflow, time.Millisecond)

			input := tc.input
			input.Cancel = true
			wfEnv.ExecuteWorkflow(retiredEnvCleanupParentWorkflow, input)

			require.True(t, wfEnv.IsWorkflowCompleted())
			err := wfEnv.GetWorkflowError()
			require.True(t, temporal.IsCanceledError(err), "expected cancellation, got %v", err)

			var wantWorktrees []cleanedWorktree
			var wantDeleted []env.DeleteSandboxInput
			for _, entry := range tc.wantWorktrees {
				wantWorktrees = append(wantWorktrees, cleanedWorktreeOf(entry, cancelCleanupReason))
				wantDeleted = append(wantDeleted, deletedSandboxOf(entry))
			}
			require.Equal(t, wantWorktrees, recorder.worktrees)
			require.Equal(t, wantDeleted, recorder.deleted)
			require.Empty(t, recorder.stopped)
		})
	}
}

func TestRetiredEnvCleanupAtFlowEnd(t *testing.T) {
	t.Parallel()
	retiredWorktree := retiredModalEnv(retiredWorktreeDir, "side--modal-retired", true)
	otherWorktree := retiredModalEnv(switchTestTargetDir, "side--modal-other", true)
	retiredInPlace := retiredModalEnv("/root/repo", "side--modal-in-place", false)

	tests := []struct {
		name          string
		retired       []retiredEnv
		failing       []string
		wantWorktrees []retiredEnv
		wantDeleted   []retiredEnv
		wantStopped   []env.StopSandboxInput
	}{
		{
			name: "nothing retired schedules no cleanup",
		},
		{
			name:          "a retired worktree env has its worktree cleaned up and sandbox deleted",
			retired:       []retiredEnv{retiredWorktree},
			wantWorktrees: []retiredEnv{retiredWorktree},
			wantDeleted:   []retiredEnv{retiredWorktree},
		},
		{
			name:        "a retired in-place env has its sandbox stopped",
			retired:     []retiredEnv{retiredInPlace},
			wantStopped: []env.StopSandboxInput{{EnvType: env.EnvTypeModal, SandboxName: "side--modal-in-place"}},
		},
		{
			name:          "cleanup failures do not stop later cleanup",
			retired:       []retiredEnv{retiredWorktree, retiredInPlace, otherWorktree},
			failing:       []string{retiredWorktreeDir, "side--modal-in-place"},
			wantWorktrees: []retiredEnv{retiredWorktree, otherWorktree},
			wantDeleted:   []retiredEnv{retiredWorktree, otherWorktree},
			wantStopped:   []env.StopSandboxInput{{EnvType: env.EnvTypeModal, SandboxName: "side--modal-in-place"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvGateTestEnv(t)
			recorder := &envCleanupRecorder{}
			mockEnvCleanup(wfEnv, recorder, tc.failing...)

			wfEnv.ExecuteWorkflow(retiredEnvCleanupTestWorkflow, retiredEnvCleanupTestInput{Retired: tc.retired})

			require.True(t, wfEnv.IsWorkflowCompleted())
			require.NoError(t, wfEnv.GetWorkflowError())

			var wantWorktrees []cleanedWorktree
			for _, entry := range tc.wantWorktrees {
				wantWorktrees = append(wantWorktrees, cleanedWorktreeOf(entry, flowEndCleanupReason))
			}
			var wantDeleted []env.DeleteSandboxInput
			for _, entry := range tc.wantDeleted {
				wantDeleted = append(wantDeleted, deletedSandboxOf(entry))
			}
			require.Equal(t, wantWorktrees, recorder.worktrees, "retired envs must be cleaned up verbatim, not redirected to the flow env")
			require.Equal(t, wantDeleted, recorder.deleted)
			require.Equal(t, tc.wantStopped, recorder.stopped)
		})
	}
}

// retiredEnvMergeTestWorkflow auto-merges a local worktree flow that
// previously ran on the retired envs.
func retiredEnvMergeTestWorkflow(ctx workflow.Context, retired []retiredEnv) (MergeApprovalResponse, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	state := newFlowEnvState(globalState)
	for _, entry := range retired {
		previous := entry.EnvContainer
		state.publish(&previous)
	}
	container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"}}
	state.publish(&container)
	for _, entry := range retired {
		state.retire(entry)
	}

	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId:  "test-workspace",
			Context:      env.WithEnvTracker(ctx, state),
			FlowScope:    &flow_action.FlowScope{SubflowName: "test-subflow"},
			GlobalState:  globalState,
			EnvContainer: &container,
		},
		Worktree: &domain.Worktree{Name: "side/sub-task"},
	}
	startBranch := "main"
	_, mergeInfo, _, err := mergeWorktreeIfApproved(dCtx, MergeWithReviewParams{
		CommitRequired: true,
		Requirements:   "Implement the thing",
		StartBranch:    &startBranch,
		AutoMerge:      true,
	}, "", "")
	return mergeInfo, err
}

func TestMergedFlowCleansUpRetiredEnvs(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvGateTestEnv(t)
	recorder := &envCleanupRecorder{}
	mockEnvCleanup(wfEnv, recorder)

	var fa *flow_action.FlowActivities
	wfEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	var srvActivities srv.Activities
	wfEnv.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	wfEnv.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
	var ima *DevAgentManagerActivities
	wfEnv.OnActivity(ima.GetFlowTaskTitle, mock.Anything, mock.Anything).Return(GetFlowTaskTitleOutput{Title: "Do the thing"}, nil).Maybe()
	wfEnv.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	wfEnv.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("diff content", nil).Maybe()
	wfEnv.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).Return("tree-hash", nil).Maybe()
	var ca *coding.CodingActivities
	wfEnv.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "diff content"}, nil).Maybe()
	wfEnv.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return("commit-sha", nil).Maybe()
	wfEnv.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).Return(git.MergeActivityResult{}, nil)

	retired := retiredModalEnv(retiredWorktreeDir, "side--modal-retired", true)
	wfEnv.ExecuteWorkflow(retiredEnvMergeTestWorkflow, []retiredEnv{retired})

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	active := cleanedWorktree{
		Env:    env.EnvIdentity{EnvType: env.EnvTypeLocal, WorkingDirectory: "/tmp/test-repo"},
		Path:   "/tmp/test-repo",
		Branch: "side/sub-task",
		Reason: flowEndCleanupReason,
	}
	require.Equal(t, []cleanedWorktree{active, cleanedWorktreeOf(retired, flowEndCleanupReason)}, recorder.worktrees)
	require.Equal(t, []env.DeleteSandboxInput{deletedSandboxOf(retired)}, recorder.deleted)
	require.Empty(t, recorder.stopped)
}

// tempLocalExecContextDuringSwitchWorkflow uses a temp local exec context
// built from the flow ctx while a switch is in progress, after the flow moved
// off the local repo env, and returns the env its activity received.
func tempLocalExecContextDuringSwitchWorkflow(ctx workflow.Context, repoDir string) (env.EnvIdentity, error) {
	ctx = utils.NoRetryCtx(ctx)
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	state := newFlowEnvState(globalState)
	localRepoEnv := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	state.publish(&localRepoEnv)
	flowEnv := switchTestSourceEnv()
	state.publish(&flowEnv)
	state.setEnvSwitch(envSwitchState{From: string(env.EnvTypeDevPod), To: string(env.EnvTypeModal)})

	tempCtx, err := newTempLocalExecContext(env.WithEnvTracker(ctx, state), "workspace-id", repoDir, defaultWorkspaceProfileId, nil, common.LLMConfig{}, common.EmbeddingConfig{})
	if err != nil {
		return env.EnvIdentity{}, err
	}
	var userConfig git.GitUserConfig
	if err := workflow.ExecuteActivity(tempCtx, git.GetGitUserConfigActivity, *tempCtx.EnvContainer).Get(tempCtx, &userConfig); err != nil {
		return env.EnvIdentity{}, err
	}
	return env.EnvIdentity{EnvType: env.EnvType(userConfig.Name), WorkingDirectory: userConfig.Email}, nil
}

func TestTempLocalExecContextTargetsTheLocalRepoEnvVerbatim(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvGateTestEnv(t)
	wfEnv.SetTestTimeout(10 * time.Second)
	wfEnv.OnActivity(git.GetGitUserConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, container env.EnvContainer) (git.GitUserConfig, error) {
			identity := env.IdentityOf(container)
			return git.GitUserConfig{Name: string(identity.EnvType), Email: identity.WorkingDirectory}, nil
		})

	repoDir := t.TempDir()
	wfEnv.ExecuteWorkflow(tempLocalExecContextDuringSwitchWorkflow, repoDir)

	require.True(t, wfEnv.IsWorkflowCompleted(), "the local repo env must not wait for the flow's env switch")
	require.NoError(t, wfEnv.GetWorkflowError())
	var received env.EnvIdentity
	require.NoError(t, wfEnv.GetWorkflowResult(&received))
	require.Equal(t, env.EnvIdentity{EnvType: env.EnvTypeLocal, WorkingDirectory: repoDir}, received)
}
