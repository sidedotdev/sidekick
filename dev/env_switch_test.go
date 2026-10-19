package dev

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/temporalmeta"
)

const (
	switchTestBranch    = "side/switch-test"
	switchTestSourceDir = "/workspaces/repo-worktrees/side-switch-test"
	switchTestTargetDir = "/root/worktrees/side-switch-test"
	switchTestHead      = "0123456789abcdef"
)

var switchTestBundle = env.SwitchBundle{
	Branch:         switchTestBranch,
	HeadCommit:     switchTestHead,
	LocalRepoDir:   "/host/repo",
	WIPArchivePath: "/host/tmp/wip.tgz",
}

type envSwitchWorkflowResult struct {
	EnvContainer       env.EnvContainer
	WorktreeDir        string
	Retired            []retiredEnv
	Lineage            []env.EnvIdentity
	SwitchInProgress   bool
	CommandPermissions common.CommandPermissionConfig
	PortForwards       []common.PortForwardConfig
}

func switchTestSourceEnv() env.EnvContainer {
	return env.EnvContainer{Env: &env.DevPodEnv{
		WorkingDirectory: switchTestSourceDir,
		WorkspaceName:    "repo",
		LocalRepoDir:     "/host/repo",
	}}
}

type envSwitchTestInput struct {
	// InPlace runs the flow directly on the repo checkout, without a flow
	// worktree, as setup does in in-place repo mode.
	InPlace bool
	Retired []retiredEnv
}

var worktreeFlow = envSwitchTestInput{}

// envSwitchTestWorkflow runs a devpod flow whose env can be switched via
// update_env, reporting what the flow body observes once switching ends.
func envSwitchTestWorkflow(ctx workflow.Context, input envSwitchTestInput) (envSwitchWorkflowResult, error) {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	container := switchTestSourceEnv()
	state := newFlowEnvState(globalState)
	state.publish(&container)
	for _, retired := range input.Retired {
		state.retire(retired)
	}
	recordCommandPermissionSources(globalState, commandPermissionSources{
		Repo: common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "repo-cmd"}}},
	})

	var worktree *domain.Worktree
	if !input.InPlace {
		worktree = &domain.Worktree{
			Id:               "wt_switch",
			FlowId:           "flow_switch",
			Name:             switchTestBranch,
			WorkspaceId:      "ws_switch",
			WorkingDirectory: switchTestSourceDir,
		}
	}
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:      env.WithEnvTracker(ctx, state),
			WorkspaceId:  "ws_switch",
			EnvContainer: &container,
			GlobalState:  globalState,
			FlowScope:    &flow_action.FlowScope{},
		},
		Worktree: worktree,
		RepoConfig: common.RepoConfig{
			CommandPermissions: common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "merged-at-setup"}}},
		},
	}
	// The flow body keeps working with its own copy of the DevContext.
	flowCtx := dCtx
	if err := SetupEnvSettingsHandlers(dCtx); err != nil {
		return envSwitchWorkflowResult{}, err
	}

	_ = workflow.Sleep(ctx, 100*time.Millisecond)
	_ = workflow.Await(ctx, func() bool {
		return workflow.AllHandlersFinished(ctx) && !state.SwitchInProgress()
	})

	worktreeDir := ""
	if flowCtx.Worktree != nil {
		worktreeDir = flowCtx.Worktree.WorkingDirectory
	}
	portForwards, _ := globalState.GetValue(globalStateKeyPortForwards).([]common.PortForwardConfig)
	return envSwitchWorkflowResult{
		EnvContainer:       *flowCtx.EnvContainer,
		WorktreeDir:        worktreeDir,
		Retired:            state.retiredEnvs(),
		Lineage:            state.lineage(),
		SwitchInProgress:   state.SwitchInProgress(),
		CommandPermissions: effectiveCommandPermissions(flowCtx),
		PortForwards:       portForwards,
	}, nil
}

type envSwitchRecorder struct {
	mu sync.Mutex
	// switchStarted, when set, is closed once the env_switch flow action is
	// persisted as started.
	switchStarted      chan struct{}
	switchStartedOnce  sync.Once
	events             []string
	actions            []domain.FlowAction
	exportInputs       []env.ExportForSwitchInput
	worktreeInputs     []env.CreateRemoteWorktreeInput
	persistedWorktrees []domain.Worktree
	restoreInputs      []env.RestoreFromSwitchInput
}

func (r *envSwitchRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *envSwitchRecorder) recordSwitchStarted() {
	r.record("switch started")
	if r.switchStarted != nil {
		r.switchStartedOnce.Do(func() { close(r.switchStarted) })
	}
}

func (r *envSwitchRecorder) lastSwitchAction(t *testing.T) domain.FlowAction {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.actions) - 1; i >= 0; i-- {
		if r.actions[i].ActionType == ActionTypeEnvSwitch {
			return r.actions[i]
		}
	}
	t.Fatal("no env_switch flow action was persisted")
	return domain.FlowAction{}
}

// mockSwitchToModal mocks everything a devpod → modal switch runs except
// VerifySwitchActivity. exportGate, when set, holds the export until closed.
func mockSwitchToModal(wfEnv *testsuite.TestWorkflowEnvironment, recorder *envSwitchRecorder, exportGate <-chan struct{}) {
	var fa *flow_action.FlowActivities
	wfEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Return(func(_ context.Context, action domain.FlowAction) error {
			if action.ActionType == ActionTypeEnvSwitch && action.ActionStatus == domain.ActionStatusStarted {
				recorder.recordSwitchStarted()
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.actions = append(recorder.actions, action)
			return nil
		})
	var meta *temporalmeta.TemporalMetaActivities
	wfEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef(nil), nil).Maybe()

	wfEnv.OnActivity(env.ExportForSwitchActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.ExportForSwitchInput) (env.ExportForSwitchOutput, error) {
			if exportGate != nil {
				select {
				case <-exportGate:
				case <-time.After(5 * time.Second):
				}
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.exportInputs = append(recorder.exportInputs, input)
			bundle := switchTestBundle
			bundle.Branch = input.Branch
			return env.ExportForSwitchOutput{Bundle: bundle}, nil
		}).Once()
	wfEnv.OnActivity(env.CreateSandboxActivity, mock.Anything, mock.Anything).
		Return(env.CreateSandboxOutput{SandboxName: "side--modal-switch", SSHHost: "modal.host", SSHPort: 2222}, nil).Once()
	wfEnv.OnActivity(env.SyncRepoToRemoteActivity, mock.Anything, mock.Anything).
		Return(env.SyncRepoToRemoteOutput{RemoteRepoDir: "/root/repo"}, nil).Once()
	wfEnv.OnActivity(env.DeepenRepoActivity, mock.Anything, mock.Anything).
		Return(env.DeepenRepoOutput{}, nil).Maybe()
	wfEnv.OnActivity(env.CreateRemoteWorktreeActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.CreateRemoteWorktreeInput) (env.CreateRemoteWorktreeOutput, error) {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.worktreeInputs = append(recorder.worktreeInputs, input)
			return env.CreateRemoteWorktreeOutput{WorktreePath: switchTestTargetDir}, nil
		}).Maybe()
	var srvActivities srv.Activities
	wfEnv.OnActivity(srvActivities.PersistWorktree, mock.Anything, mock.Anything).
		Return(func(_ context.Context, worktree domain.Worktree) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.persistedWorktrees = append(recorder.persistedWorktrees, worktree)
			return nil
		}).Maybe()
	wfEnv.OnActivity(env.RestoreFromSwitchActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.RestoreFromSwitchInput) (env.RestoreFromSwitchOutput, error) {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.restoreInputs = append(recorder.restoreInputs, input)
			return env.RestoreFromSwitchOutput{}, nil
		}).Once()
	wfEnv.OnActivity(common.BaseCommandPermissionsActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input common.BaseCommandPermissionsInput) (common.CommandPermissionConfig, error) {
			return common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "base-" + input.EnvType}}}, nil
		}).Maybe()
}

func newEnvSwitchTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	return newEnvSettingsTestEnv(t)
}

func commandPatterns(config common.CommandPermissionConfig) []string {
	var patterns []string
	for _, p := range config.AutoApprove {
		patterns = append(patterns, p.Pattern)
	}
	return patterns
}

func TestEnvSwitchPublishesProvisionedEnvToTheFlow(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	mockSwitchToModal(wfEnv, recorder, nil)

	callbacks := &envUpdateCallbacks{}
	updateCompletedBeforeVerify := false
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.VerifySwitchInput) (env.VerifySwitchOutput, error) {
			updateCompletedBeforeVerify = callbacks.completed
			return env.VerifySwitchOutput{Head: input.ExpectedHead}, nil
		}).Once()

	desired := EnvSettings{
		EnvType:      string(env.EnvTypeModal),
		PortForwards: []common.PortForwardConfig{{HostPort: 3000}},
		Modal:        common.ModalEnvConfig{Memory: 2048},
	}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "switch-to-modal", callbacks, desired)
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, worktreeFlow)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)
	require.NotEmpty(t, callbacks.result.FlowActionId)
	require.True(t, updateCompletedBeforeVerify, "the update must return without waiting for the switch")
	require.Equal(t, string(env.EnvTypeDevPod), callbacks.result.Settings.EnvType)

	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.False(t, result.SwitchInProgress)

	modalEnv, ok := result.EnvContainer.Env.(*env.ModalEnv)
	require.True(t, ok, "flow env should be the Modal env, got %T", result.EnvContainer.Env)
	require.Equal(t, switchTestTargetDir, modalEnv.WorkingDirectory)
	require.Equal(t, "side--modal-switch", modalEnv.SandboxName)
	require.Equal(t, "/host/repo", modalEnv.LocalRepoDir)
	require.Equal(t, desired.PortForwards, modalEnv.PortForwards)
	require.Equal(t, switchTestTargetDir, result.WorktreeDir)
	require.Equal(t, desired.PortForwards, result.PortForwards)

	require.Len(t, result.Retired, 1)
	require.Equal(t, env.IdentityOf(switchTestSourceEnv()), env.IdentityOf(result.Retired[0].EnvContainer))
	require.NotNil(t, result.Retired[0].Worktree)
	require.Equal(t, switchTestSourceDir, result.Retired[0].Worktree.WorkingDirectory)
	require.Equal(t, switchTestBranch, result.Retired[0].Worktree.Name)
	require.ElementsMatch(t, []env.EnvIdentity{
		env.IdentityOf(switchTestSourceEnv()),
		env.IdentityOf(result.EnvContainer),
	}, result.Lineage)

	require.Equal(t, []string{"base-modal", "repo-cmd"}, commandPatterns(result.CommandPermissions),
		"permissions must be re-derived from the unmerged sources for the new env type")

	require.Len(t, recorder.exportInputs, 1)
	require.Equal(t, switchTestBranch, recorder.exportInputs[0].Branch)
	require.Equal(t, env.IdentityOf(switchTestSourceEnv()), env.IdentityOf(recorder.exportInputs[0].EnvContainer))
	require.Len(t, recorder.worktreeInputs, 1)
	require.Equal(t, switchTestBranch, recorder.worktreeInputs[0].BranchName)
	require.Equal(t, switchTestBranch, recorder.worktreeInputs[0].StartBranch)
	require.True(t, recorder.worktreeInputs[0].AllowReuse)
	require.Len(t, recorder.persistedWorktrees, 1)
	require.Equal(t, "wt_switch", recorder.persistedWorktrees[0].Id)
	require.Equal(t, switchTestTargetDir, recorder.persistedWorktrees[0].WorkingDirectory)
	require.Len(t, recorder.restoreInputs, 1)
	require.Equal(t, switchTestBundle, recorder.restoreInputs[0].Bundle)

	action := recorder.lastSwitchAction(t)
	require.Equal(t, callbacks.result.FlowActionId, action.Id)
	require.Equal(t, domain.ActionStatusComplete, action.ActionStatus)
	require.Equal(t, string(env.EnvTypeDevPod), action.ActionParams["from"])
	require.Equal(t, string(env.EnvTypeModal), action.ActionParams["to"])
	require.Contains(t, action.ActionResult, switchTestHead)
}

func TestFailedEnvSwitchKeepsPreviousEnvAndRetiresPartialTarget(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	mockSwitchToModal(wfEnv, recorder, nil)
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
		Return(env.VerifySwitchOutput{}, temporal.NewNonRetryableApplicationError("switched env HEAD is deadbeef", "VerifyFailed", nil)).Once()

	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "failing-switch", callbacks, EnvSettings{EnvType: string(env.EnvTypeModal)})
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, worktreeFlow)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)

	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.False(t, result.SwitchInProgress)
	require.Equal(t, env.IdentityOf(switchTestSourceEnv()), env.IdentityOf(result.EnvContainer))
	require.Equal(t, switchTestSourceDir, result.WorktreeDir)
	require.Equal(t, []string{"merged-at-setup"}, commandPatterns(result.CommandPermissions))

	require.Len(t, result.Retired, 1)
	partial, ok := result.Retired[0].EnvContainer.Env.(*env.ModalEnv)
	require.True(t, ok, "partial target should be retired, got %T", result.Retired[0].EnvContainer.Env)
	require.Equal(t, switchTestTargetDir, partial.WorkingDirectory)
	require.Equal(t, "side--modal-switch", partial.SandboxName)
	require.NotNil(t, result.Retired[0].Worktree)
	require.Equal(t, switchTestTargetDir, result.Retired[0].Worktree.WorkingDirectory)

	require.NotEmpty(t, recorder.persistedWorktrees)
	lastPersisted := recorder.persistedWorktrees[len(recorder.persistedWorktrees)-1]
	require.Equal(t, switchTestSourceDir, lastPersisted.WorkingDirectory, "the worktree record must point back at the active env")

	action := recorder.lastSwitchAction(t)
	require.Equal(t, domain.ActionStatusFailed, action.ActionStatus)
	require.Contains(t, action.ActionResult, "switched env HEAD is deadbeef")
}

func TestInPlaceEnvSwitchMovesTheCheckedOutBranch(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	mockSwitchToModal(wfEnv, recorder, nil)
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
		Return(env.VerifySwitchOutput{Head: switchTestHead}, nil).Once()

	const checkedOut = "feature/checked-out"
	var branchQueries []env.EnvRunCommandActivityInput
	wfEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			branchQueries = append(branchQueries, input)
			return env.EnvRunCommandActivityOutput{Stdout: checkedOut + "\n"}, nil
		}).Once()

	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "in-place-switch", callbacks, EnvSettings{EnvType: string(env.EnvTypeModal)})
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{InPlace: true})

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)

	require.Len(t, branchQueries, 1)
	require.Equal(t, "git", branchQueries[0].Command)
	require.Equal(t, []string{"rev-parse", "--abbrev-ref", "HEAD"}, branchQueries[0].Args)
	require.Equal(t, env.IdentityOf(switchTestSourceEnv()), env.IdentityOf(branchQueries[0].EnvContainer))
	require.Len(t, recorder.exportInputs, 1)
	require.Equal(t, checkedOut, recorder.exportInputs[0].Branch)
	require.Len(t, recorder.restoreInputs, 1)
	require.Equal(t, checkedOut, recorder.restoreInputs[0].Bundle.Branch, "the checked-out branch must carry over to the new env")
	require.Empty(t, recorder.worktreeInputs, "in-place switches must not create worktrees")
	require.Empty(t, recorder.persistedWorktrees, "in-place switches must not persist worktree records")

	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	modalEnv, ok := result.EnvContainer.Env.(*env.ModalEnv)
	require.True(t, ok, "flow env should be the Modal env, got %T", result.EnvContainer.Env)
	require.Equal(t, "/root/repo", modalEnv.WorkingDirectory)
	require.Empty(t, result.WorktreeDir)
	require.Len(t, result.Retired, 1)
	require.Equal(t, env.IdentityOf(switchTestSourceEnv()), env.IdentityOf(result.Retired[0].EnvContainer))
	require.Nil(t, result.Retired[0].Worktree)
	require.Equal(t, domain.ActionStatusComplete, recorder.lastSwitchAction(t).ActionStatus)
}

func TestEnvSwitchToRetiredEnvKeepsOneRetiredEntryPerEnv(t *testing.T) {
	t.Parallel()
	previousTarget := retiredEnv{
		EnvContainer: env.EnvContainer{Env: &env.ModalEnv{
			WorkingDirectory: switchTestTargetDir,
			SandboxName:      "side--modal-previous",
			LocalRepoDir:     "/host/repo",
		}},
		Worktree: &domain.Worktree{Id: "wt_switch", Name: switchTestBranch, WorkingDirectory: switchTestTargetDir},
	}

	tests := []struct {
		name          string
		verifyErr     error
		wantRetiredIs env.EnvContainer
		wantSandbox   string
	}{
		{
			name:          "success leaves only the env switched away from",
			wantRetiredIs: switchTestSourceEnv(),
		},
		{
			name:          "failure replaces the earlier entry with the partial target",
			verifyErr:     temporal.NewNonRetryableApplicationError("HEAD mismatch", "VerifyFailed", nil),
			wantRetiredIs: previousTarget.EnvContainer,
			wantSandbox:   "side--modal-switch",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSwitchTestEnv(t)
			recorder := &envSwitchRecorder{}
			mockSwitchToModal(wfEnv, recorder, nil)
			wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
				Return(env.VerifySwitchOutput{Head: switchTestHead}, tc.verifyErr).Once()

			callbacks := &envUpdateCallbacks{}
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "switch-back", callbacks, EnvSettings{EnvType: string(env.EnvTypeModal)})
			}, 5*time.Millisecond)

			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Retired: []retiredEnv{previousTarget}})

			require.True(t, wfEnv.IsWorkflowCompleted())
			require.NoError(t, wfEnv.GetWorkflowError())
			callbacks.requireApplied(t)

			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Len(t, result.Retired, 1)
			require.Equal(t, env.IdentityOf(tc.wantRetiredIs), env.IdentityOf(result.Retired[0].EnvContainer))
			if tc.wantSandbox != "" {
				require.Equal(t, tc.wantSandbox, result.Retired[0].EnvContainer.Env.(*env.ModalEnv).SandboxName)
			}
		})
	}
}

func TestEnvSwitchWaitsForInFlightConfigUpdate(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	mockSwitchToModal(wfEnv, recorder, nil)
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
		Return(env.VerifySwitchOutput{Head: switchTestHead}, nil).Once()

	// The reconciliation stays in flight long enough for the switch to
	// arrive, and ends early if the switch wrongly starts meanwhile.
	recorder.switchStarted = make(chan struct{})
	reconciledForwards := []common.PortForwardConfig{{HostPort: 4000}}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			select {
			case <-recorder.switchStarted:
			case <-time.After(300 * time.Millisecond):
			}
			devPodEnv := *input.EnvContainer.Env.(*env.DevPodEnv)
			devPodEnv.PortForwards = input.Desired.PortForwards
			recorder.record("config reconciled")
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &devPodEnv}, Config: input.Desired}, nil
		}).Once()

	configCallbacks := &envUpdateCallbacks{}
	switchCallbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "config-update", configCallbacks,
			EnvSettings{EnvType: string(env.EnvTypeDevPod), PortForwards: reconciledForwards})
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "switch-to-modal", switchCallbacks, EnvSettings{EnvType: string(env.EnvTypeModal)})
	}, 6*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, worktreeFlow)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	configCallbacks.requireApplied(t)
	switchCallbacks.requireApplied(t)
	require.Equal(t, []string{"config reconciled", "switch started"}, recorder.events,
		"a switch must not start while a config update is still changing the env")

	require.Len(t, recorder.exportInputs, 1)
	exported, ok := recorder.exportInputs[0].EnvContainer.Env.(*env.DevPodEnv)
	require.True(t, ok)
	require.Equal(t, reconciledForwards, exported.PortForwards, "the switch must export the env the config update published")

	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	_, isModal := result.EnvContainer.Env.(*env.ModalEnv)
	require.True(t, isModal)
	require.Len(t, result.Retired, 1)
	retired, ok := result.Retired[0].EnvContainer.Env.(*env.DevPodEnv)
	require.True(t, ok)
	require.Equal(t, reconciledForwards, retired.PortForwards)
}

func TestConcurrentEnvSwitchIsRejected(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	secondSubmitted := make(chan struct{})
	mockSwitchToModal(wfEnv, recorder, secondSubmitted)
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, mock.Anything).
		Return(env.VerifySwitchOutput{Head: switchTestHead}, nil).Once()

	first := &envUpdateCallbacks{}
	second := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "first-switch", first, EnvSettings{EnvType: string(env.EnvTypeModal)})
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "second-switch", second, EnvSettings{EnvType: string(env.EnvTypeOpenShell)})
		close(secondSubmitted)
	}, 6*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, worktreeFlow)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	first.requireApplied(t)
	require.False(t, second.accepted)
	require.ErrorContains(t, second.rejection, "already in progress")

	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	_, isModal := result.EnvContainer.Env.(*env.ModalEnv)
	require.True(t, isModal)
}
