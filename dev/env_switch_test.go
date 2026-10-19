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
	"sidekick/utils"
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
	Probes             []envSwitchProbeResult
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
	Source  *env.EnvContainer
	Probes  int
}

var worktreeFlow = envSwitchTestInput{}

// envSwitchTestWorkflow reports what the flow body observes while its
// environment is changed by update_env.
func envSwitchTestWorkflow(ctx workflow.Context, input envSwitchTestInput) (envSwitchWorkflowResult, error) {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	container := switchTestSourceEnv()
	if input.Source != nil {
		container = *input.Source
	}
	original := container
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
			WorkingDirectory: container.Env.GetWorkingDirectory(),
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

	var probes []envSwitchProbeResult
	if err := workflow.SetQueryHandler(ctx, "switchProbeCount", func() (int, error) {
		return len(probes), nil
	}); err != nil {
		return envSwitchWorkflowResult{}, err
	}
	for range input.Probes {
		var duringSwitch bool
		workflow.GetSignalChannel(ctx, "probe").Receive(ctx, &duringSwitch)
		if duringSwitch {
			if err := workflow.Await(ctx, state.SwitchInProgress); err != nil {
				return envSwitchWorkflowResult{}, err
			}
		}
		probe, err := probeSwitchEnv(flowCtx, original)
		if err != nil {
			return envSwitchWorkflowResult{}, err
		}
		probes = append(probes, probe)
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
		Probes:             probes,
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
	return newEnvGateTestEnv(t)
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

type envSwitchProbeResult struct {
	StartedWhileSwitching bool
	PinnedWhileSwitching  bool
	Resolved              string
	Pinned                string
	After                 string
	Current               env.EnvContainer
	Retired               []retiredEnv
	Lineage               []env.EnvIdentity
	SwitchInProgress      bool
}

func probeSwitchEnv(dCtx DevContext, original env.EnvContainer) (envSwitchProbeResult, error) {
	ctx := utils.NoRetryCtx(dCtx.Context)
	state := newFlowEnvState(dCtx.GlobalState)
	result := envSwitchProbeResult{StartedWhileSwitching: state.SwitchInProgress()}
	started, finished := false, false
	var regularOutput env.EnvRunCommandActivityOutput
	var regularErr error
	workflow.Go(ctx, func(gCtx workflow.Context) {
		started = true
		regularErr = workflow.ExecuteActivity(gCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: original,
			Command:      "probe",
		}).Get(gCtx, &regularOutput)
		finished = true
	})
	if err := workflow.Await(ctx, func() bool { return started }); err != nil {
		return result, err
	}

	result.PinnedWhileSwitching = state.SwitchInProgress()
	var pinnedOutput env.EnvRunCommandActivityOutput
	if err := workflow.ExecuteActivity(env.WithPinnedEnv(ctx), env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer: original,
		Command:      "pinned",
	}).Get(ctx, &pinnedOutput); err != nil {
		return result, err
	}
	if err := workflow.Await(ctx, func() bool {
		return finished && !state.SwitchInProgress() && workflow.AllHandlersFinished(ctx)
	}); err != nil {
		return result, err
	}
	if regularErr != nil {
		return result, regularErr
	}

	var afterOutput env.EnvRunCommandActivityOutput
	if err := workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer: *dCtx.EnvContainer,
		Command:      "after",
	}).Get(ctx, &afterOutput); err != nil {
		return result, err
	}
	result.Resolved = regularOutput.Stdout
	result.Pinned = pinnedOutput.Stdout
	result.After = afterOutput.Stdout
	result.Current = *dCtx.EnvContainer
	result.Retired = append([]retiredEnv(nil), state.retiredEnvs()...)
	result.Lineage = append([]env.EnvIdentity(nil), state.lineage()...)
	result.SwitchInProgress = state.SwitchInProgress()
	return result, nil
}

type envSwitchProbeCallbacks struct {
	envUpdateCallbacks
	onComplete func()
}

func (c *envSwitchProbeCallbacks) Complete(result interface{}, err error) {
	c.envUpdateCallbacks.Complete(result, err)
	if err == nil && c.onComplete != nil {
		c.onComplete()
	}
}

func switchTestLocalEnv() env.EnvContainer {
	return env.EnvContainer{Env: &env.LocalGitWorktreeEnv{
		WorkingDirectory: "/host/repo-worktrees/side-switch-test",
	}}
}

func switchTestEnvDescription(container env.EnvContainer) string {
	return string(container.Env.GetType()) + "@" + container.Env.GetWorkingDirectory()
}

func mockSwitchBookkeeping(wfEnv *testsuite.TestWorkflowEnvironment, recorder *envSwitchRecorder) {
	var fa *flow_action.FlowActivities
	wfEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Return(func(_ context.Context, action domain.FlowAction) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.actions = append(recorder.actions, action)
			if action.ActionType == ActionTypeEnvSwitch {
				recorder.events = append(recorder.events, string(action.ActionStatus))
			}
			return nil
		})
	var meta *temporalmeta.TemporalMetaActivities
	wfEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef(nil), nil).Maybe()
	var srvActivities srv.Activities
	wfEnv.OnActivity(srvActivities.PersistWorktree, mock.Anything, mock.Anything).
		Return(func(_ context.Context, worktree domain.Worktree) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.persistedWorktrees = append(recorder.persistedWorktrees, worktree)
			return nil
		}).Maybe()
	wfEnv.OnActivity(common.BaseCommandPermissionsActivity, mock.Anything, mock.Anything).
		Return(common.CommandPermissionConfig{}, nil).Maybe()
}

func mockSwitchTransfer(wfEnv *testsuite.TestWorkflowEnvironment, recorder *envSwitchRecorder, source, target env.EnvContainer, gate <-chan struct{}, verifyErr error) {
	wfEnv.OnActivity(env.ExportForSwitchActivity, mock.Anything, env.ExportForSwitchInput{
		EnvContainer: source,
		Branch:       switchTestBranch,
	}).Return(func(ctx context.Context, input env.ExportForSwitchInput) (env.ExportForSwitchOutput, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return env.ExportForSwitchOutput{}, ctx.Err()
		case <-time.After(3 * time.Second):
			return env.ExportForSwitchOutput{}, temporal.NewNonRetryableApplicationError("pinned probe did not release export", "test-timeout", nil)
		}
		recorder.record("exported")
		return env.ExportForSwitchOutput{Bundle: switchTestBundle}, nil
	}).Once()
	wfEnv.OnActivity(env.RestoreFromSwitchActivity, mock.Anything, env.RestoreFromSwitchInput{
		EnvContainer: target,
		Bundle:       switchTestBundle,
	}).Return(func(context.Context, env.RestoreFromSwitchInput) (env.RestoreFromSwitchOutput, error) {
		recorder.record("restored")
		return env.RestoreFromSwitchOutput{}, nil
	}).Once()
	wfEnv.OnActivity(env.VerifySwitchActivity, mock.Anything, env.VerifySwitchInput{
		EnvContainer: target,
		ExpectedHead: switchTestHead,
	}).Return(func(context.Context, env.VerifySwitchInput) (env.VerifySwitchOutput, error) {
		recorder.record("verified")
		return env.VerifySwitchOutput{Head: switchTestHead}, verifyErr
	}).Once()
}

func mockSwitchDevPodProvisioning(wfEnv *testsuite.TestWorkflowEnvironment) env.EnvContainer {
	target := env.EnvContainer{Env: &env.DevPodEnv{
		WorkingDirectory: switchTestSourceDir,
		WorkspaceName:    env.DevPodWorkspaceName(switchTestBundle.LocalRepoDir),
		LocalRepoDir:     switchTestBundle.LocalRepoDir,
	}}
	wfEnv.OnActivity(env.DevPodUpActivity, mock.Anything, env.DevPodUpInput{
		WorkspacePath: switchTestBundle.LocalRepoDir,
	}).Return(nil).Once()
	base := env.EnvContainer{Env: &env.DevPodEnv{
		WorkingDirectory: "/workspaces/repo",
		WorkspaceName:    env.DevPodWorkspaceName(switchTestBundle.LocalRepoDir),
		LocalRepoDir:     switchTestBundle.LocalRepoDir,
	}}
	wfEnv.OnActivity(env.CreateDevPodWorktreeActivity, mock.Anything, env.CreateDevPodWorktreeInput{
		EnvContainer: base,
		RepoDir:      "/workspaces/repo",
		BranchName:   switchTestBranch,
		StartBranch:  switchTestBranch,
		WorkspaceId:  "ws_switch",
		AllowReuse:   true,
	}).Return(env.CreateDevPodWorktreeOutput{WorktreePath: switchTestSourceDir}, nil).Once()
	return target
}

func mockSwitchProbes(wfEnv *testsuite.TestWorkflowEnvironment, recorder *envSwitchRecorder, original, resolved env.EnvContainer, gate chan struct{}) {
	for _, command := range []string{"pinned", "probe", "after"} {
		expected := resolved
		if command == "pinned" {
			expected = original
		}
		wfEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, env.EnvRunCommandActivityInput{
			EnvContainer: expected,
			Command:      command,
		}).Return(func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			recorder.record(input.Command)
			if input.Command == "pinned" && gate != nil {
				close(gate)
			}
			return env.EnvRunCommandActivityOutput{Stdout: switchTestEnvDescription(input.EnvContainer)}, nil
		}).Once()
	}
}

func TestEnvSwitchLocalWorktreeActivityGate(t *testing.T) {
	t.Parallel()
	for _, failVerification := range []bool{false, true} {
		name := "publishes verified devpod"
		if failVerification {
			name = "reverts after failed verification"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSwitchTestEnv(t)
			recorder := &envSwitchRecorder{}
			mockSwitchBookkeeping(wfEnv, recorder)
			source := switchTestLocalEnv()
			target := mockSwitchDevPodProvisioning(wfEnv)
			expected, retired := target, source
			status := domain.ActionStatusComplete
			var verifyErr error
			if failVerification {
				expected, retired = source, target
				status = domain.ActionStatusFailed
				verifyErr = temporal.NewNonRetryableApplicationError("switched env HEAD is deadbeef", "verify", nil)
			}
			gate := make(chan struct{})
			mockSwitchTransfer(wfEnv, recorder, source, target, gate, verifyErr)
			mockSwitchProbes(wfEnv, recorder, source, expected, gate)

			callbacks := &envSwitchProbeCallbacks{onComplete: func() {
				recorder.record("update returned")
				wfEnv.SignalWorkflow("probe", true)
			}}
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "switch-to-devpod", callbacks, EnvSettings{EnvType: string(env.EnvTypeDevPod)})
			}, 5*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &source, Probes: 1})

			require.NoError(t, wfEnv.GetWorkflowError())
			callbacks.requireApplied(t)
			require.NotEmpty(t, callbacks.result.FlowActionId)
			require.Equal(t, string(env.EnvTypeLocal), callbacks.result.Settings.EnvType)
			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Equal(t, expected, result.EnvContainer)
			require.Equal(t, expected.Env.GetWorkingDirectory(), result.WorktreeDir)
			require.False(t, result.SwitchInProgress)
			require.Len(t, result.Retired, 1)
			require.Equal(t, retired, result.Retired[0].EnvContainer)
			require.NotNil(t, result.Retired[0].Worktree)
			require.Equal(t, retired.Env.GetWorkingDirectory(), result.Retired[0].Worktree.WorkingDirectory)
			require.Equal(t, switchTestBranch, result.Retired[0].Worktree.Name)
			expectedLineage := []env.EnvIdentity{env.IdentityOf(source)}
			if !failVerification {
				expectedLineage = append(expectedLineage, env.IdentityOf(target))
			}
			require.Equal(t, expectedLineage, result.Lineage)
			require.Len(t, result.Probes, 1)
			probe := result.Probes[0]
			require.True(t, probe.StartedWhileSwitching)
			require.True(t, probe.PinnedWhileSwitching)
			require.Equal(t, switchTestEnvDescription(source), probe.Pinned)
			require.Equal(t, switchTestEnvDescription(expected), probe.Resolved)
			require.Equal(t, switchTestEnvDescription(expected), probe.After)
			require.False(t, probe.SwitchInProgress)

			action := recorder.lastSwitchAction(t)
			require.Equal(t, callbacks.result.FlowActionId, action.Id)
			require.Equal(t, status, action.ActionStatus)
			require.Equal(t, "local", action.ActionParams["from"])
			require.Equal(t, "devpod", action.ActionParams["to"])
			if failVerification {
				require.Contains(t, action.ActionResult, "switched env HEAD is deadbeef")
			} else {
				require.Contains(t, action.ActionResult, switchTestHead)
			}
			require.Equal(t, []string{
				string(domain.ActionStatusStarted), "update returned", "pinned", "exported",
				"restored", "verified", string(status), "probe", "after",
			}, recorder.events)
			require.NotEmpty(t, recorder.persistedWorktrees)
			for _, worktree := range recorder.persistedWorktrees {
				require.Equal(t, "wt_switch", worktree.Id)
				require.Equal(t, "flow_switch", worktree.FlowId)
				require.Equal(t, "ws_switch", worktree.WorkspaceId)
				require.Equal(t, switchTestBranch, worktree.Name)
			}
			require.Equal(t, result.WorktreeDir, recorder.persistedWorktrees[len(recorder.persistedWorktrees)-1].WorkingDirectory)
		})
	}
}

func mockSwitchOpenShellProvisioning(wfEnv *testsuite.TestWorkflowEnvironment) env.EnvContainer {
	const sandboxName = "side--openshell-switch"
	newContainer := func(dir string) env.EnvContainer {
		return env.EnvContainer{Env: &env.OpenShellEnv{
			WorkingDirectory: dir,
			SandboxName:      sandboxName,
			LocalRepoDir:     switchTestBundle.LocalRepoDir,
		}}
	}
	wfEnv.OnActivity(env.CheckSandboxActivity, mock.Anything, env.CheckSandboxInput{
		EnvType:     env.EnvTypeOpenShell,
		SandboxName: env.OpenShellSandboxName(switchTestBundle.LocalRepoDir),
	}).Return(env.CheckSandboxOutput{}, nil).Once()
	wfEnv.OnActivity(env.CreateSandboxActivity, mock.Anything, mock.MatchedBy(func(input env.CreateSandboxInput) bool {
		return input.EnvType == env.EnvTypeOpenShell &&
			input.Name == env.OpenShellSandboxName(switchTestBundle.LocalRepoDir) &&
			input.RepoDir == switchTestBundle.LocalRepoDir
	})).Return(env.CreateSandboxOutput{SandboxName: sandboxName}, nil).Once()
	wfEnv.OnActivity(env.SyncRepoToRemoteActivity, mock.Anything, env.SyncRepoToRemoteInput{
		EnvContainer: newContainer(""),
		LocalRepoDir: switchTestBundle.LocalRepoDir,
		Branches:     []string{switchTestBranch},
	}).Return(env.SyncRepoToRemoteOutput{RemoteRepoDir: "/root/repo"}, nil).Once()
	wfEnv.OnActivity(env.DeepenRepoActivity, mock.Anything, mock.Anything).
		Return(env.DeepenRepoOutput{}, nil).Maybe()
	wfEnv.OnActivity(env.CreateRemoteWorktreeActivity, mock.Anything, env.CreateRemoteWorktreeInput{
		EnvContainer: newContainer("/root/repo"),
		RepoDir:      "/root/repo",
		BranchName:   switchTestBranch,
		StartBranch:  switchTestBranch,
		WorkspaceId:  "ws_switch",
		LocalRepoDir: switchTestBundle.LocalRepoDir,
		AllowReuse:   true,
	}).Return(env.CreateRemoteWorktreeOutput{WorktreePath: switchTestTargetDir}, nil).Once()
	return newContainer(switchTestTargetDir)
}

func registerSwitchProbeUpdates(t *testing.T, wfEnv *testsuite.TestWorkflowEnvironment, desired []EnvSettings) []*envSwitchProbeCallbacks {
	t.Helper()
	callbacks := make([]*envSwitchProbeCallbacks, len(desired))
	for i := range callbacks {
		callbacks[i] = &envSwitchProbeCallbacks{onComplete: func() {
			wfEnv.SignalWorkflow("probe", true)
		}}
	}

	index, polls := 0, 0
	var next func()
	next = func() {
		encoded, err := wfEnv.QueryWorkflow("switchProbeCount")
		require.NoError(t, err)
		var count int
		require.NoError(t, encoded.Get(&count))
		if count != index {
			polls++
			require.Less(t, polls, 1000, "previous switch probe did not finish")
			wfEnv.RegisterDelayedCallback(next, 10*time.Millisecond)
			return
		}
		polls = 0
		wfEnv.UpdateWorkflow(UpdateNameEnv, "", callbacks[index], desired[index])
		index++
		if index < len(desired) {
			wfEnv.RegisterDelayedCallback(next, 10*time.Millisecond)
		}
	}
	wfEnv.RegisterDelayedCallback(next, 5*time.Millisecond)
	return callbacks
}

func TestEnvSwitchChainedLineageAndRetiredReuse(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	recorder := &envSwitchRecorder{}
	mockSwitchBookkeeping(wfEnv, recorder)
	local := switchTestLocalEnv()
	devpod := mockSwitchDevPodProvisioning(wfEnv)
	openshell := mockSwitchOpenShellProvisioning(wfEnv)
	require.Equal(t, devpod, mockSwitchDevPodProvisioning(wfEnv))

	sources := []env.EnvContainer{local, devpod, openshell}
	targets := []env.EnvContainer{devpod, openshell, devpod}
	desired := make([]EnvSettings, len(targets))
	for i, target := range targets {
		gate := make(chan struct{})
		mockSwitchTransfer(wfEnv, recorder, sources[i], target, gate, nil)
		mockSwitchProbes(wfEnv, recorder, local, target, gate)
		desired[i] = EnvSettings{EnvType: string(target.Env.GetType())}
	}
	callbacks := registerSwitchProbeUpdates(t, wfEnv, desired)
	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &local, Probes: len(targets)})

	require.NoError(t, wfEnv.GetWorkflowError())
	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Len(t, result.Probes, 3)
	expectedRetired := [][]env.EnvContainer{
		{local},
		{local, devpod},
		{local, openshell},
	}
	expectedLineage := []env.EnvIdentity{env.IdentityOf(local), env.IdentityOf(devpod), env.IdentityOf(openshell)}
	actionIDs := make(map[string]bool)
	for i, probe := range result.Probes {
		callbacks[i].requireApplied(t)
		id := callbacks[i].result.FlowActionId
		require.NotEmpty(t, id)
		require.False(t, actionIDs[id], "each switch needs its own flow action")
		actionIDs[id] = true
		from := string(sources[i].Env.GetType())
		if i == 0 {
			from = string(env.EnvTypeLocal)
		}
		require.Equal(t, from, callbacks[i].result.Settings.EnvType)
		require.True(t, probe.StartedWhileSwitching)
		require.True(t, probe.PinnedWhileSwitching)
		require.Equal(t, switchTestEnvDescription(local), probe.Pinned)
		require.Equal(t, switchTestEnvDescription(targets[i]), probe.Resolved)
		require.Equal(t, probe.Resolved, probe.After)
		require.Equal(t, targets[i], probe.Current)
		require.False(t, probe.SwitchInProgress)
		lineage := expectedLineage
		if i == 0 {
			lineage = lineage[:2]
		}
		require.Equal(t, lineage, probe.Lineage)
		require.Len(t, probe.Retired, len(expectedRetired[i]))
		for j, retired := range probe.Retired {
			require.Equal(t, expectedRetired[i][j], retired.EnvContainer)
			require.NotNil(t, retired.Worktree)
			require.Equal(t, retired.EnvContainer.Env.GetWorkingDirectory(), retired.Worktree.WorkingDirectory)
			require.Equal(t, "wt_switch", retired.Worktree.Id)
			require.Equal(t, switchTestBranch, retired.Worktree.Name)
		}
	}

	require.Equal(t, devpod, result.EnvContainer)
	require.Equal(t, switchTestSourceDir, result.WorktreeDir)
	require.Equal(t, result.Probes[2].Retired, result.Retired)
	require.Equal(t, expectedLineage, result.Lineage)
	require.Len(t, recorder.persistedWorktrees, 3)
	require.Equal(t, recorder.persistedWorktrees[0], recorder.persistedWorktrees[2],
		"returning to DevPod must reattach the original worktree")
	require.Len(t, recorder.actions, 6)
	for i, callbacks := range callbacks {
		started, completed := recorder.actions[2*i], recorder.actions[2*i+1]
		require.Equal(t, callbacks.result.FlowActionId, started.Id)
		require.Equal(t, started.Id, completed.Id)
		require.Equal(t, ActionTypeEnvSwitch, completed.ActionType)
		require.Equal(t, domain.ActionStatusStarted, started.ActionStatus)
		require.Equal(t, domain.ActionStatusComplete, completed.ActionStatus)
		require.Equal(t, callbacks.result.Settings.EnvType, completed.ActionParams["from"])
		require.Equal(t, desired[i].EnvType, completed.ActionParams["to"])
		require.Contains(t, completed.ActionResult, switchTestHead)
	}
}

func TestEnvSwitchSettingsValidatorFromLocalWorktree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		desired EnvSettings
		message string
	}{
		{
			name:    "invalid environment type",
			desired: EnvSettings{EnvType: "mainframe"},
			message: `invalid environment type "mainframe"`,
		},
		{
			name: "invalid modal config",
			desired: EnvSettings{
				EnvType: "modal",
				Modal: common.ModalEnvConfig{Volumes: []common.ModalVolumeMount{
					{Name: "a", MountPath: "/cache"},
					{Name: "b", MountPath: "/cache/"},
				}},
			},
			message: "configured more than once",
		},
		{
			name: "invalid port forwards",
			desired: EnvSettings{
				EnvType: "devpod",
				PortForwards: []common.PortForwardConfig{
					{HostPort: 3000, ContainerPort: 4000},
					{HostPort: 4000},
				},
			},
			message: "container_port 4000 is used by both",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSwitchTestEnv(t)
			source := switchTestLocalEnv()
			callbacks := &envUpdateCallbacks{}
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "", callbacks, test.desired)
			}, 5*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &source})

			require.NoError(t, wfEnv.GetWorkflowError())
			require.False(t, callbacks.accepted)
			require.False(t, callbacks.completed)
			require.ErrorContains(t, callbacks.rejection, test.message)
			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Equal(t, source, result.EnvContainer)
			require.Empty(t, result.Retired)
			require.False(t, result.SwitchInProgress)
			require.Equal(t, []env.EnvIdentity{env.IdentityOf(source)}, result.Lineage)
		})
	}
}

func TestEnvSwitchSameTypeReconcilesSynchronously(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSwitchTestEnv(t)
	source := switchTestSourceEnv()
	desired := EnvSettings{
		EnvType:      "devpod",
		PortForwards: []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 3001}},
	}
	updated := *source.Env.(*env.DevPodEnv)
	updated.PortForwards = desired.PortForwards
	target := env.EnvContainer{Env: &updated}
	recorder := &envSwitchRecorder{}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, env.UpdateEnvConfigInput{
		EnvContainer: source,
		Current:      env.EnvConfig{},
		Desired:      env.EnvConfig{PortForwards: desired.PortForwards},
	}).Return(func(context.Context, env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
		recorder.record("reconciled")
		return env.UpdateEnvConfigOutput{
			EnvContainer: target,
			Config:       env.EnvConfig{PortForwards: desired.PortForwards},
		}, nil
	}).Once()
	mockSwitchProbes(wfEnv, recorder, source, target, nil)
	callbacks := &envSwitchProbeCallbacks{onComplete: func() {
		recorder.record("update returned")
		wfEnv.SignalWorkflow("probe", false)
	}}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "", callbacks, desired)
	}, 5*time.Millisecond)
	wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Probes: 1})

	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)
	require.Empty(t, callbacks.result.FlowActionId)
	require.Equal(t, desired, callbacks.result.Settings)
	require.GreaterOrEqual(t, len(recorder.events), 2)
	require.Equal(t, []string{"reconciled", "update returned"}, recorder.events[:2])
	var result envSwitchWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, target, result.EnvContainer)
	require.Equal(t, desired.PortForwards, result.PortForwards)
	require.Empty(t, result.Retired)
	require.False(t, result.SwitchInProgress)
	require.Equal(t, []env.EnvIdentity{env.IdentityOf(source)}, result.Lineage)
	require.Len(t, result.Probes, 1)
	require.False(t, result.Probes[0].StartedWhileSwitching)
	require.False(t, result.Probes[0].PinnedWhileSwitching)
}
