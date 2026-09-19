package dev

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

type modalConfigWorkflowResult struct {
	Config       common.ModalEnvConfig
	PortForwards []common.PortForwardConfig
	SandboxName  string
	SSHHost      string
	SSHPort      int
	EnvForwards  []common.PortForwardConfig
}

type modalConfigWorkflowInput struct {
	Modal        common.ModalEnvConfig
	PortForwards []common.PortForwardConfig
}

func modalConfigTestWorkflow(ctx workflow.Context, input modalConfigWorkflowInput) (modalConfigWorkflowResult, error) {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	envContainer := &env.EnvContainer{Env: &env.ModalEnv{
		WorkingDirectory: "/root/repo",
		SandboxName:      "side--repo-abc",
		SSHHost:          "old.modal.host",
		SSHPort:          1111,
		LocalRepoDir:     "/host/repo",
		PortForwards:     input.PortForwards,
	}}
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:      ctx,
			GlobalState:  globalState,
			EnvContainer: envContainer,
		},
		RepoConfig: common.RepoConfig{ModalConfig: input.Modal},
	}

	if err := SetupModalConfigHandlers(dCtx); err != nil {
		return modalConfigWorkflowResult{}, err
	}

	// Give the test's delayed updates time to arrive, then let every
	// accepted one finish before reporting the state they left behind.
	_ = workflow.Sleep(ctx, 100*time.Millisecond)
	_ = workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) })

	modalEnv := envContainer.Env.(*env.ModalEnv)
	config, _ := globalState.GetValue(globalStateKeyModalConfig).(common.ModalEnvConfig)
	portForwards, _ := globalState.GetValue(globalStateKeyPortForwards).([]common.PortForwardConfig)
	return modalConfigWorkflowResult{
		Config:       config,
		PortForwards: portForwards,
		SandboxName:  modalEnv.SandboxName,
		SSHHost:      modalEnv.SSHHost,
		SSHPort:      modalEnv.SSHPort,
		EnvForwards:  modalEnv.PortForwards,
	}, nil
}

func newModalConfigTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var workflowSuite testsuite.WorkflowTestSuite
	wfEnv := workflowSuite.NewTestWorkflowEnvironment()
	wfEnv.SetWorkerOptions(utils.TestWorkerOptions())
	t.Cleanup(func() { wfEnv.AssertExpectations(t) })
	return wfEnv
}

func requireUpdateApplied(t *testing.T, callbacks *modelConfigUpdateCallbacks) {
	t.Helper()
	require.True(t, callbacks.accepted)
	require.NoError(t, callbacks.rejection)
	require.True(t, callbacks.completed)
	require.NoError(t, callbacks.err)
}

func TestModalConfigUpdateReconcilesSandboxAndRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	validUpdate := common.ModalEnvConfig{Memory: 2048, MemoryLimit: 8192}
	invalidUpdate := common.ModalEnvConfig{
		Volumes: []common.ModalVolumeMount{
			{Name: "a", MountPath: "/cache"},
			{Name: "b", MountPath: "/cache/"},
		},
	}

	recreatedEnvContainer := env.EnvContainer{Env: &env.ModalEnv{
		WorkingDirectory: "/root/repo",
		SandboxName:      "side--repo-abc",
		SSHHost:          "new.modal.host",
		SSHPort:          2222,
		LocalRepoDir:     "/host/repo",
		PortForwards:     initial.PortForwards,
	}}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.MatchedBy(func(input env.UpdateEnvConfigInput) bool {
		return input.Current.Modal.Memory == initial.Modal.Memory &&
			len(input.Current.PortForwards) == 1 &&
			input.Desired.Modal.Memory == validUpdate.Memory &&
			input.Desired.Modal.MemoryLimit == validUpdate.MemoryLimit &&
			len(input.Desired.PortForwards) == 1
	})).Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
		return env.UpdateEnvConfigOutput{EnvContainer: recreatedEnvContainer, Config: input.Desired}, nil
	}).Once()

	invalidCallbacks := &modelConfigUpdateCallbacks{}
	validCallbacks := &modelConfigUpdateCallbacks{}
	var queriedConfig common.ModalEnvConfig
	var queriedForwards PortForwardsConfig
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "invalid-modal-config-update", invalidCallbacks, invalidUpdate)
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "valid-modal-config-update", validCallbacks, validUpdate)
	}, 10*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		value, err := wfEnv.QueryWorkflow(QueryNameModalConfig)
		require.NoError(t, err)
		require.NoError(t, value.Get(&queriedConfig))
		value, err = wfEnv.QueryWorkflow(QueryNamePortForwards)
		require.NoError(t, err)
		require.NoError(t, value.Get(&queriedForwards))
	}, 50*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	// The invalid configuration must be rejected by the validator, before any
	// reconciliation activity runs.
	require.False(t, invalidCallbacks.accepted)
	require.Error(t, invalidCallbacks.rejection)
	require.Contains(t, invalidCallbacks.rejection.Error(), "configured more than once")

	requireUpdateApplied(t, validCallbacks)

	require.Equal(t, validUpdate, queriedConfig)
	require.Equal(t, initial.PortForwards, queriedForwards.PortForwards)
	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, validUpdate, result.Config)
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, "side--repo-abc", result.SandboxName)
	require.Equal(t, "new.modal.host", result.SSHHost)
	require.Equal(t, 2222, result.SSHPort)
}

func TestModalConfigUpdateLegacyVersionRecreatesSandboxDirectly(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)
	wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	initial := modalConfigWorkflowInput{Modal: common.ModalEnvConfig{Memory: 1024}}
	update := common.ModalEnvConfig{Memory: 2048}
	recreatedEnvContainer := env.EnvContainer{Env: &env.ModalEnv{
		WorkingDirectory: "/root/repo",
		SandboxName:      "side--repo-abc",
		SSHHost:          "new.modal.host",
		SSHPort:          2222,
		LocalRepoDir:     "/host/repo",
	}}
	wfEnv.OnActivity(env.ModalRecreateSandboxActivity, mock.Anything, mock.MatchedBy(func(input env.ModalRecreateSandboxInput) bool {
		return input.Config.Memory == update.Memory
	})).Return(env.ModalRecreateSandboxOutput{EnvContainer: recreatedEnvContainer}, nil).Once()

	callbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "legacy-modal-config-update", callbacks, update)
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, callbacks)

	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, update, result.Config)
	require.Equal(t, "new.modal.host", result.SSHHost)
}

func TestPortForwardsUpdateIsRefusedOnLegacyVersionExecutions(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)
	wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	update := PortForwardsConfig{PortForwards: []common.PortForwardConfig{{HostPort: 3000}}}
	reconcileCalls := 0
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			reconcileCalls++
			return env.UpdateEnvConfigOutput{EnvContainer: input.EnvContainer, Config: input.Desired}, nil
		}).Maybe()

	callbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "legacy-port-forwards-update", callbacks, update)
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	require.True(t, callbacks.completed)
	require.Error(t, callbacks.err)
	require.Contains(t, callbacks.err.Error(), "start a new flow")
	require.Zero(t, reconcileCalls)

	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, initial.PortForwards, result.EnvForwards)
}

func TestPortForwardsUpdateAppliesMappingsAndRejectsInvalidLists(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	validUpdate := PortForwardsConfig{PortForwards: []common.PortForwardConfig{
		{HostPort: 3000, ContainerPort: 3001},
		{HostPort: 5432},
	}}
	invalidUpdate := PortForwardsConfig{PortForwards: []common.PortForwardConfig{
		{HostPort: 3000, ContainerPort: 4000},
		{HostPort: 4000},
	}}

	var activityInput env.UpdateEnvConfigInput
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			activityInput = input
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			modalEnv.PortForwards = input.Desired.PortForwards
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}, Config: input.Desired}, nil
		}).Once()

	invalidCallbacks := &modelConfigUpdateCallbacks{}
	validCallbacks := &modelConfigUpdateCallbacks{}
	var queriedForwards PortForwardsConfig
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "invalid-port-forwards-update", invalidCallbacks, invalidUpdate)
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "valid-port-forwards-update", validCallbacks, validUpdate)
	}, 10*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		value, err := wfEnv.QueryWorkflow(QueryNamePortForwards)
		require.NoError(t, err)
		require.NoError(t, value.Get(&queriedForwards))
	}, 50*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	require.False(t, invalidCallbacks.accepted)
	require.Error(t, invalidCallbacks.rejection)
	require.Contains(t, invalidCallbacks.rejection.Error(), "container_port 4000 is used by both")

	requireUpdateApplied(t, validCallbacks)
	require.Equal(t, initial.PortForwards, activityInput.Current.PortForwards)
	require.Equal(t, initial.Modal, activityInput.Current.Modal)
	require.Equal(t, validUpdate.PortForwards, activityInput.Desired.PortForwards)
	require.Equal(t, initial.Modal, activityInput.Desired.Modal, "a port update must not touch sandbox settings")

	require.Equal(t, validUpdate.PortForwards, queriedForwards.PortForwards)
	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, validUpdate.PortForwards, result.PortForwards)
	require.Equal(t, validUpdate.PortForwards, result.EnvForwards)
	require.Equal(t, initial.Modal, result.Config)
	require.Equal(t, "side--repo-abc", result.SandboxName)
}

func TestPortForwardsUpdateRetryStartsFromUnchangedPublishedState(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	update := PortForwardsConfig{PortForwards: []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 3001}}}

	var inputs []env.UpdateEnvConfigInput
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			inputs = append(inputs, input)
			if len(inputs) == 1 {
				return env.UpdateEnvConfigOutput{}, errors.New("ssh connection reset")
			}
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			modalEnv.PortForwards = input.Desired.PortForwards
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}, Config: input.Desired}, nil
		}).Twice()

	callbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "retried-port-forwards-update", callbacks, update)
	}, 5*time.Millisecond)

	// The retry policy backs off for a second before the second attempt, so
	// a query in between observes what the failed attempt left published.
	var forwardsDuringBackoff PortForwardsConfig
	wfEnv.RegisterDelayedCallback(func() {
		require.Len(t, inputs, 1, "first attempt should have failed by now")
		require.False(t, callbacks.completed)
		value, err := wfEnv.QueryWorkflow(QueryNamePortForwards)
		require.NoError(t, err)
		require.NoError(t, value.Get(&forwardsDuringBackoff))
	}, 500*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, callbacks)
	require.Equal(t, initial.PortForwards, forwardsDuringBackoff.PortForwards)

	require.Len(t, inputs, 2)
	for _, input := range inputs {
		require.Equal(t, initial.PortForwards, input.Current.PortForwards)
		require.Equal(t, initial.Modal, input.Current.Modal)
		require.Equal(t, update.PortForwards, input.Desired.PortForwards)
		require.Equal(t, initial.Modal, input.Desired.Modal)
		require.Equal(t, "side--repo-abc", input.EnvContainer.Env.(*env.ModalEnv).SandboxName)
	}

	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, update.PortForwards, result.PortForwards)
	require.Equal(t, update.PortForwards, result.EnvForwards)
	require.Equal(t, initial.Modal, result.Config)
}

func TestPortForwardsUpdateFailureKeepsPublishedState(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	update := PortForwardsConfig{PortForwards: []common.PortForwardConfig{{HostPort: 3000}}}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(env.UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError("remote port busy", "ReplaceFailed", nil)).Once()

	callbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "failing-port-forwards-update", callbacks, update)
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	require.True(t, callbacks.accepted)
	require.True(t, callbacks.completed)
	require.Error(t, callbacks.err)
	require.Contains(t, callbacks.err.Error(), "remote port busy")

	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, initial.PortForwards, result.EnvForwards)
	require.Equal(t, "old.modal.host", result.SSHHost)
}

func TestConcurrentModalAndPortForwardUpdatesAreSerialized(t *testing.T) {
	t.Parallel()
	wfEnv := newModalConfigTestEnv(t)

	initial := modalConfigWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	modalUpdate := common.ModalEnvConfig{Memory: 4096}
	portUpdate := PortForwardsConfig{PortForwards: []common.PortForwardConfig{{HostPort: 3000}}}

	// The first reconciliation stays in flight until the second update has
	// been submitted, so both handlers contend for the environment.
	portUpdateSubmitted := make(chan struct{})
	var inputs []env.UpdateEnvConfigInput
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			inputs = append(inputs, input)
			if len(inputs) == 1 {
				select {
				case <-portUpdateSubmitted:
				case <-time.After(5 * time.Second):
					t.Error("port forwards update was never submitted while the Modal update was in flight")
				}
			}
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			modalEnv.PortForwards = input.Desired.PortForwards
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}, Config: input.Desired}, nil
		}).Twice()

	modalCallbacks := &modelConfigUpdateCallbacks{}
	portCallbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "modal-config-update", modalCallbacks, modalUpdate)
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNamePortForwards, "port-forwards-update", portCallbacks, portUpdate)
		close(portUpdateSubmitted)
	}, 6*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, modalCallbacks)
	requireUpdateApplied(t, portCallbacks)

	require.Len(t, inputs, 2)
	require.Equal(t, initial.Modal, inputs[0].Current.Modal)
	require.Equal(t, modalUpdate, inputs[0].Desired.Modal)
	require.Equal(t, inputs[0].Desired, inputs[1].Current, "second update must start from what the first left in effect")
	require.Equal(t, modalUpdate, inputs[1].Desired.Modal)
	require.Equal(t, portUpdate.PortForwards, inputs[1].Desired.PortForwards)

	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, modalUpdate, result.Config)
	require.Equal(t, portUpdate.PortForwards, result.PortForwards)
	require.Equal(t, portUpdate.PortForwards, result.EnvForwards)
}
