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

type envUpdateCallbacks struct {
	accepted  bool
	rejection error
	completed bool
	result    EnvUpdateResult
	err       error
}

func (c *envUpdateCallbacks) Accept() { c.accepted = true }

func (c *envUpdateCallbacks) Reject(err error) { c.rejection = err }

func (c *envUpdateCallbacks) Complete(success interface{}, err error) {
	c.completed = true
	c.err = err
	if result, ok := success.(EnvUpdateResult); ok {
		c.result = result
	}
}

func (c *envUpdateCallbacks) requireApplied(t *testing.T) {
	t.Helper()
	require.True(t, c.accepted)
	require.NoError(t, c.rejection)
	require.True(t, c.completed)
	require.NoError(t, c.err)
}

func requireUpdateApplied(t *testing.T, callbacks *modelConfigUpdateCallbacks) {
	t.Helper()
	require.True(t, callbacks.accepted)
	require.NoError(t, callbacks.rejection)
	require.True(t, callbacks.completed)
	require.NoError(t, callbacks.err)
}

type envSettingsWorkflowResult struct {
	Config       common.ModalEnvConfig
	PortForwards []common.PortForwardConfig
	SandboxName  string
	SSHHost      string
	SSHPort      int
	EnvForwards  []common.PortForwardConfig
}

type envSettingsWorkflowInput struct {
	Modal        common.ModalEnvConfig
	PortForwards []common.PortForwardConfig
}

func envSettingsTestWorkflow(ctx workflow.Context, input envSettingsWorkflowInput) (envSettingsWorkflowResult, error) {
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

	if err := SetupEnvSettingsHandlers(dCtx); err != nil {
		return envSettingsWorkflowResult{}, err
	}

	// Give the test's delayed updates time to arrive, then let every
	// accepted one finish before reporting the state they left behind.
	_ = workflow.Sleep(ctx, 100*time.Millisecond)
	_ = workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) })

	modalEnv := envContainer.Env.(*env.ModalEnv)
	config, _ := globalState.GetValue(globalStateKeyModalConfig).(common.ModalEnvConfig)
	portForwards, _ := globalState.GetValue(globalStateKeyPortForwards).([]common.PortForwardConfig)
	return envSettingsWorkflowResult{
		Config:       config,
		PortForwards: portForwards,
		SandboxName:  modalEnv.SandboxName,
		SSHHost:      modalEnv.SSHHost,
		SSHPort:      modalEnv.SSHPort,
		EnvForwards:  modalEnv.PortForwards,
	}, nil
}

func newEnvSettingsTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var workflowSuite testsuite.WorkflowTestSuite
	wfEnv := workflowSuite.NewTestWorkflowEnvironment()
	wfEnv.SetWorkerOptions(utils.TestWorkerOptions())
	t.Cleanup(func() { wfEnv.AssertExpectations(t) })
	return wfEnv
}

func modalSettings(modal common.ModalEnvConfig, portForwards []common.PortForwardConfig) EnvSettings {
	return EnvSettings{EnvType: string(env.EnvTypeModal), Modal: modal, PortForwards: portForwards}
}

func TestEnvSettingsUpdateReconcilesModalSandboxAndRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
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

	invalidCallbacks := &envUpdateCallbacks{}
	validCallbacks := &envUpdateCallbacks{}
	var queried EnvSettings
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "invalid-modal-config-update", invalidCallbacks, modalSettings(invalidUpdate, initial.PortForwards))
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "valid-modal-config-update", validCallbacks, modalSettings(validUpdate, initial.PortForwards))
	}, 10*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		value, err := wfEnv.QueryWorkflow(QueryNameEnv)
		require.NoError(t, err)
		require.NoError(t, value.Get(&queried))
	}, 50*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	// The invalid configuration must be rejected by the validator, before any
	// reconciliation activity runs.
	require.False(t, invalidCallbacks.accepted)
	require.Error(t, invalidCallbacks.rejection)
	require.Contains(t, invalidCallbacks.rejection.Error(), "configured more than once")

	validCallbacks.requireApplied(t)
	require.Empty(t, validCallbacks.result.FlowActionId)
	require.Equal(t, modalSettings(validUpdate, initial.PortForwards), validCallbacks.result.Settings)

	require.Equal(t, modalSettings(validUpdate, initial.PortForwards), queried)
	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, validUpdate, result.Config)
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, "side--repo-abc", result.SandboxName)
	require.Equal(t, "new.modal.host", result.SSHHost)
	require.Equal(t, 2222, result.SSHPort)
}

func TestEnvSettingsUpdateRejectsInvalidEnvType(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{Modal: common.ModalEnvConfig{Memory: 1024}}
	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "invalid-env-type-update", callbacks, EnvSettings{EnvType: "mainframe", Modal: initial.Modal})
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	require.False(t, callbacks.accepted)
	require.Error(t, callbacks.rejection)
	require.Contains(t, callbacks.rejection.Error(), `invalid environment type "mainframe"`)
}

func TestEnvSettingsUpdateWithUnchangedSettingsSkipsReconciliation(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	reconcileCalls := 0
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			reconcileCalls++
			return env.UpdateEnvConfigOutput{EnvContainer: input.EnvContainer, Config: input.Desired}, nil
		}).Maybe()

	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "unchanged-update", callbacks, modalSettings(initial.Modal, initial.PortForwards))
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)
	require.Zero(t, reconcileCalls)
	require.Equal(t, modalSettings(initial.Modal, initial.PortForwards), callbacks.result.Settings)
}

func TestLegacyModalConfigUpdateRecreatesSandboxDirectly(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)
	wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	initial := envSettingsWorkflowInput{Modal: common.ModalEnvConfig{Memory: 1024}}
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

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, callbacks)

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, update, result.Config)
	require.Equal(t, "new.modal.host", result.SSHHost)
}

func TestLegacyModalConfigUpdateReconcilesThroughEnvConfigActivity(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)
	wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	update := common.ModalEnvConfig{Memory: 4096}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.MatchedBy(func(input env.UpdateEnvConfigInput) bool {
		return input.Current.Modal.Memory == initial.Modal.Memory && input.Desired.Modal.Memory == update.Memory
	})).Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
		return env.UpdateEnvConfigOutput{EnvContainer: input.EnvContainer, Config: input.Desired}, nil
	}).Once()

	callbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "legacy-reconciled-update", callbacks, update)
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, callbacks)

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, update, result.Config)
	require.Equal(t, initial.PortForwards, result.PortForwards)
}

func TestLegacyPortForwardsUpdateIsRefusedOnLegacyConfigUpdateExecutions(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)
	wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	initial := envSettingsWorkflowInput{
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

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	require.True(t, callbacks.completed)
	require.Error(t, callbacks.err)
	require.Contains(t, callbacks.err.Error(), "start a new flow")
	require.Zero(t, reconcileCalls)

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, initial.PortForwards, result.EnvForwards)
}

func TestEnvSettingsUpdateAppliesPortForwardsAndRejectsInvalidLists(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	validForwards := []common.PortForwardConfig{
		{HostPort: 3000, ContainerPort: 3001},
		{HostPort: 5432},
	}
	invalidForwards := []common.PortForwardConfig{
		{HostPort: 3000, ContainerPort: 4000},
		{HostPort: 4000},
	}

	var activityInput env.UpdateEnvConfigInput
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			activityInput = input
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			modalEnv.PortForwards = input.Desired.PortForwards
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}, Config: input.Desired}, nil
		}).Once()

	invalidCallbacks := &envUpdateCallbacks{}
	validCallbacks := &envUpdateCallbacks{}
	var queried EnvSettings
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "invalid-port-forwards-update", invalidCallbacks, modalSettings(initial.Modal, invalidForwards))
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "valid-port-forwards-update", validCallbacks, modalSettings(initial.Modal, validForwards))
	}, 10*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		value, err := wfEnv.QueryWorkflow(QueryNameEnv)
		require.NoError(t, err)
		require.NoError(t, value.Get(&queried))
	}, 50*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	require.False(t, invalidCallbacks.accepted)
	require.Error(t, invalidCallbacks.rejection)
	require.Contains(t, invalidCallbacks.rejection.Error(), "container_port 4000 is used by both")

	validCallbacks.requireApplied(t)
	require.Equal(t, initial.PortForwards, activityInput.Current.PortForwards)
	require.Equal(t, initial.Modal, activityInput.Current.Modal)
	require.Equal(t, validForwards, activityInput.Desired.PortForwards)
	require.Equal(t, initial.Modal, activityInput.Desired.Modal, "a port update must not touch sandbox settings")

	require.Equal(t, validForwards, queried.PortForwards)
	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, validForwards, result.PortForwards)
	require.Equal(t, validForwards, result.EnvForwards)
	require.Equal(t, initial.Modal, result.Config)
	require.Equal(t, "side--repo-abc", result.SandboxName)
}

func TestEnvSettingsUpdateRetryStartsFromUnchangedPublishedState(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	updatedForwards := []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 3001}}

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

	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "retried-port-forwards-update", callbacks, modalSettings(initial.Modal, updatedForwards))
	}, 5*time.Millisecond)

	// The retry policy backs off for a second before the second attempt, so
	// a query in between observes what the failed attempt left published.
	var duringBackoff EnvSettings
	wfEnv.RegisterDelayedCallback(func() {
		require.Len(t, inputs, 1, "first attempt should have failed by now")
		require.False(t, callbacks.completed)
		value, err := wfEnv.QueryWorkflow(QueryNameEnv)
		require.NoError(t, err)
		require.NoError(t, value.Get(&duringBackoff))
	}, 500*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	callbacks.requireApplied(t)
	require.Equal(t, initial.PortForwards, duringBackoff.PortForwards)

	require.Len(t, inputs, 2)
	for _, input := range inputs {
		require.Equal(t, initial.PortForwards, input.Current.PortForwards)
		require.Equal(t, initial.Modal, input.Current.Modal)
		require.Equal(t, updatedForwards, input.Desired.PortForwards)
		require.Equal(t, initial.Modal, input.Desired.Modal)
		require.Equal(t, "side--repo-abc", input.EnvContainer.Env.(*env.ModalEnv).SandboxName)
	}

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, updatedForwards, result.PortForwards)
	require.Equal(t, updatedForwards, result.EnvForwards)
	require.Equal(t, initial.Modal, result.Config)
}

func TestEnvSettingsUpdateFailureKeepsPublishedState(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(env.UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError("remote port busy", "ReplaceFailed", nil)).Once()

	callbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "failing-port-forwards-update", callbacks,
			modalSettings(initial.Modal, []common.PortForwardConfig{{HostPort: 3000}}))
	}, 5*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	require.True(t, callbacks.accepted)
	require.True(t, callbacks.completed)
	require.Error(t, callbacks.err)
	require.Contains(t, callbacks.err.Error(), "remote port busy")

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, initial.PortForwards, result.PortForwards)
	require.Equal(t, initial.PortForwards, result.EnvForwards)
	require.Equal(t, "old.modal.host", result.SSHHost)
}

func TestConcurrentEnvSettingsUpdatesAreSerialized(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)

	initial := envSettingsWorkflowInput{
		Modal:        common.ModalEnvConfig{Memory: 1024},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}
	modalUpdate := common.ModalEnvConfig{Memory: 4096}
	portUpdate := []common.PortForwardConfig{{HostPort: 3000}}

	// The first reconciliation stays in flight until the second update has
	// been submitted, so both handlers contend for the environment.
	secondUpdateSubmitted := make(chan struct{})
	var inputs []env.UpdateEnvConfigInput
	wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
			inputs = append(inputs, input)
			if len(inputs) == 1 {
				select {
				case <-secondUpdateSubmitted:
				case <-time.After(5 * time.Second):
					t.Error("second update was never submitted while the first was in flight")
				}
			}
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			modalEnv.PortForwards = input.Desired.PortForwards
			return env.UpdateEnvConfigOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}, Config: input.Desired}, nil
		}).Twice()

	modalCallbacks := &envUpdateCallbacks{}
	portCallbacks := &envUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "modal-config-update", modalCallbacks, modalSettings(modalUpdate, initial.PortForwards))
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameEnv, "port-forwards-update", portCallbacks, modalSettings(modalUpdate, portUpdate))
		close(secondUpdateSubmitted)
	}, 6*time.Millisecond)

	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	modalCallbacks.requireApplied(t)
	portCallbacks.requireApplied(t)

	require.Len(t, inputs, 2)
	require.Equal(t, initial.Modal, inputs[0].Current.Modal)
	require.Equal(t, modalUpdate, inputs[0].Desired.Modal)
	require.Equal(t, inputs[0].Desired, inputs[1].Current, "second update must start from what the first left in effect")
	require.Equal(t, modalUpdate, inputs[1].Desired.Modal)
	require.Equal(t, portUpdate, inputs[1].Desired.PortForwards)

	var result envSettingsWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, modalUpdate, result.Config)
	require.Equal(t, portUpdate, result.PortForwards)
	require.Equal(t, portUpdate, result.EnvForwards)
}
