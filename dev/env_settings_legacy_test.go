package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"
)

func TestLegacyWorkflowSupportsUnifiedEnvSwitch(t *testing.T) {
	t.Parallel()
	for _, source := range []env.EnvContainer{
		switchTestLocalEnv(),
		{Env: &env.ModalEnv{
			WorkingDirectory: "/root/worktrees/source",
			SandboxName:      "side--source",
			LocalRepoDir:     "/host/repo",
		}},
	} {
		t.Run(string(source.Env.GetType()), func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSwitchTestEnv(t)
			wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			recorder := &envSwitchRecorder{}
			mockSwitchBookkeeping(wfEnv, recorder)
			target := mockSwitchDevPodProvisioning(wfEnv)
			gate := make(chan struct{})
			close(gate)
			mockSwitchTransfer(wfEnv, recorder, source, target, gate, nil)

			var queried EnvSettings
			var queryErr error
			callbacks := &envUpdateCallbacks{}
			wfEnv.RegisterDelayedCallback(func() {
				value, err := wfEnv.QueryWorkflow(QueryNameEnv)
				queryErr = err
				if err == nil {
					queryErr = value.Get(&queried)
				}
				wfEnv.UpdateWorkflow(UpdateNameEnv, "legacy-flow-switch", callbacks, EnvSettings{EnvType: string(env.EnvTypeDevPod)})
			}, 5*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &source})

			require.NoError(t, wfEnv.GetWorkflowError())
			require.NoError(t, queryErr)
			require.Equal(t, normalizedEnvType(string(source.Env.GetType())), queried.EnvType)
			callbacks.requireApplied(t)
			require.NotEmpty(t, callbacks.result.FlowActionId)
			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Equal(t, target, result.EnvContainer)
			require.Len(t, result.Retired, 1)
			require.Equal(t, source, result.Retired[0].EnvContainer)
			require.False(t, result.SwitchInProgress)
		})
	}
}

func TestLegacyAndUnifiedEnvUpdatesAreSerialized(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		legacyRecreate bool
		unifiedFirst   bool
	}{
		{name: "reconciliation first"},
		{name: "recreation first", legacyRecreate: true},
		{name: "unified before reconciliation", unifiedFirst: true},
		{name: "unified before recreation", legacyRecreate: true, unifiedFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfEnv, recorder := newEnvChangeTestEnv(t)
			wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			version := workflow.Version(1)
			if tc.legacyRecreate {
				version = workflow.DefaultVersion
			}
			wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(version)

			initial := envSettingsWorkflowInput{Modal: common.ModalEnvConfig{Memory: 1024}}
			legacyConfig := common.ModalEnvConfig{Memory: 2048}
			unifiedConfig := common.ModalEnvConfig{Memory: 4096}
			legacyDelay, unifiedDelay := 20*time.Millisecond, time.Duration(0)
			if tc.unifiedFirst {
				legacyDelay, unifiedDelay = unifiedDelay, legacyDelay
			}
			// Each update publishes an env with a distinctive host, so the
			// host each side sees tells which update ran first.
			var hostAtLegacyUpdate string
			legacyResult := func(container env.EnvContainer) env.EnvContainer {
				hostAtLegacyUpdate = container.Env.(*env.ModalEnv).SSHHost
				modalEnv := *container.Env.(*env.ModalEnv)
				modalEnv.SSHHost = "legacy.modal.host"
				return env.EnvContainer{Env: &modalEnv}
			}
			if tc.legacyRecreate {
				wfEnv.OnActivity(env.ModalRecreateSandboxActivity, mock.Anything, mock.Anything).
					After(legacyDelay).
					Return(func(_ context.Context, input env.ModalRecreateSandboxInput) (env.ModalRecreateSandboxOutput, error) {
						return env.ModalRecreateSandboxOutput{EnvContainer: legacyResult(input.EnvContainer)}, nil
					}).Once()
			} else {
				wfEnv.OnActivity(env.UpdateEnvConfigActivity, mock.Anything, mock.MatchedBy(func(input env.UpdateEnvConfigInput) bool {
					return input.Desired.Modal.Memory == legacyConfig.Memory
				})).After(legacyDelay).
					Return(func(_ context.Context, input env.UpdateEnvConfigInput) (env.UpdateEnvConfigOutput, error) {
						return env.UpdateEnvConfigOutput{EnvContainer: legacyResult(input.EnvContainer), Config: input.Desired}, nil
					}).Once()
			}

			// The unified memory change replaces the sandbox from a snapshot.
			var hostAtUnifiedUpdate string
			wfEnv.OnActivity(env.ModalReplaceSandboxActivity, mock.Anything, mock.MatchedBy(func(input env.ModalReplaceSandboxInput) bool {
				return input.Config.Memory == unifiedConfig.Memory
			})).After(unifiedDelay).Return(func(_ context.Context, input env.ModalReplaceSandboxInput) (env.ModalReplaceSandboxOutput, error) {
				modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
				hostAtUnifiedUpdate = modalEnv.SSHHost
				modalEnv.SandboxName = env.ModalReplacementSandboxName(modalEnv.SandboxName)
				modalEnv.SSHHost = "unified.modal.host"
				return env.ModalReplaceSandboxOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}}, nil
			}).Once()

			legacyCallbacks := &modelConfigUpdateCallbacks{}
			unifiedCallbacks := &envUpdateCallbacks{}
			submitLegacy := func() {
				wfEnv.UpdateWorkflow(UpdateNameModalConfig, "legacy-config", legacyCallbacks, legacyConfig)
			}
			submitUnified := func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "unified-config", unifiedCallbacks, modalSettings(unifiedConfig, nil))
			}
			first, second := submitLegacy, submitUnified
			if tc.unifiedFirst {
				first, second = second, first
			}
			wfEnv.RegisterDelayedCallback(first, 5*time.Millisecond)
			wfEnv.RegisterDelayedCallback(second, 6*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, initial)

			require.NoError(t, wfEnv.GetWorkflowError())
			requireUpdateApplied(t, legacyCallbacks)
			unifiedCallbacks.requireApplied(t)
			require.Equal(t, domain.ActionStatusComplete, recorder.lastSwitchAction(t).ActionStatus)
			var result envSettingsWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			if tc.unifiedFirst {
				require.Equal(t, "old.modal.host", hostAtUnifiedUpdate)
				require.Equal(t, "unified.modal.host", hostAtLegacyUpdate)
				require.Equal(t, legacyConfig, result.Config)
				require.Equal(t, "legacy.modal.host", result.SSHHost)
			} else {
				require.Equal(t, "old.modal.host", hostAtLegacyUpdate)
				require.Equal(t, "legacy.modal.host", hostAtUnifiedUpdate)
				require.Equal(t, unifiedConfig, result.Config)
				require.Equal(t, "unified.modal.host", result.SSHHost)
			}
			require.Len(t, result.Retired, 1, "the replaced sandbox is retired for flow-end cleanup")
			require.Equal(t, "side--repo-abc", result.Retired[0].EnvContainer.Env.(*env.ModalEnv).SandboxName)
		})
	}
}

func TestLegacyEnvUpdateRejectsNonModalAfterSwitch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		updateName     string
		input          interface{}
		legacyRecreate bool
	}{
		{name: "recreation", updateName: UpdateNameModalConfig, input: common.ModalEnvConfig{Memory: 2048}, legacyRecreate: true},
		{name: "reconciliation", updateName: UpdateNameModalConfig, input: common.ModalEnvConfig{Memory: 2048}},
		{name: "port forwards", updateName: UpdateNamePortForwards, input: PortForwardsConfig{PortForwards: []common.PortForwardConfig{{HostPort: 3000}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSwitchTestEnv(t)
			wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			version := workflow.Version(1)
			if tc.legacyRecreate {
				version = workflow.DefaultVersion
			}
			wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(version).Maybe()
			source := env.EnvContainer{Env: &env.ModalEnv{
				WorkingDirectory: "/root/worktrees/source",
				SandboxName:      "side--source",
				LocalRepoDir:     "/host/repo",
			}}
			recorder := &envSwitchRecorder{}
			mockSwitchBookkeeping(wfEnv, recorder)
			target := mockSwitchDevPodProvisioning(wfEnv)
			gate := make(chan struct{})
			mockSwitchTransfer(wfEnv, recorder, source, target, gate, nil)

			switchCallbacks := &envUpdateCallbacks{}
			during := &modelConfigUpdateCallbacks{}
			after := &modelConfigUpdateCallbacks{}
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "switch", switchCallbacks, EnvSettings{EnvType: string(env.EnvTypeDevPod)})
			}, 5*time.Millisecond)
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(tc.updateName, "during-switch", during, tc.input)
				close(gate)
			}, 6*time.Millisecond)
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(tc.updateName, "after-switch", after, tc.input)
			}, 80*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &source})

			require.NoError(t, wfEnv.GetWorkflowError())
			switchCallbacks.requireApplied(t)
			require.True(t, during.accepted)
			require.ErrorContains(t, during.err, "requires a Modal environment")
			require.False(t, after.accepted)
			require.ErrorContains(t, after.rejection, "requires a Modal environment")
			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Equal(t, target, result.EnvContainer)
		})
	}
}

func TestLegacyModalRecreationsRemainConcurrent(t *testing.T) {
	t.Parallel()
	wfEnv := newEnvSettingsTestEnv(t)
	wfEnv.OnGetVersion(envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	wfEnv.OnGetVersion(envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	first := common.ModalEnvConfig{Memory: 2048}
	second := common.ModalEnvConfig{Memory: 4096}
	var hostAtSecondUpdate string
	for _, config := range []common.ModalEnvConfig{first, second} {
		wfEnv.OnActivity(env.ModalRecreateSandboxActivity, mock.Anything, mock.MatchedBy(func(input env.ModalRecreateSandboxInput) bool {
			return input.Config.Memory == config.Memory
		})).After(20 * time.Millisecond).Return(func(_ context.Context, input env.ModalRecreateSandboxInput) (env.ModalRecreateSandboxOutput, error) {
			modalEnv := *input.EnvContainer.Env.(*env.ModalEnv)
			if input.Config.Memory == second.Memory {
				hostAtSecondUpdate = modalEnv.SSHHost
			}
			modalEnv.SSHHost = "recreated.modal.host"
			return env.ModalRecreateSandboxOutput{EnvContainer: env.EnvContainer{Env: &modalEnv}}, nil
		}).Once()
	}
	firstCallbacks := &modelConfigUpdateCallbacks{}
	secondCallbacks := &modelConfigUpdateCallbacks{}
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "first", firstCallbacks, first)
	}, 5*time.Millisecond)
	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.UpdateWorkflow(UpdateNameModalConfig, "second", secondCallbacks, second)
	}, 6*time.Millisecond)
	wfEnv.ExecuteWorkflow(envSettingsTestWorkflow, envSettingsWorkflowInput{})

	require.NoError(t, wfEnv.GetWorkflowError())
	requireUpdateApplied(t, firstCallbacks)
	requireUpdateApplied(t, secondCallbacks)
	require.Equal(t, "old.modal.host", hostAtSecondUpdate)
}
