package dev

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

type modalConfigWorkflowResult struct {
	Config      common.ModalEnvConfig
	SandboxName string
	SSHHost     string
	SSHPort     int
}

func modalConfigTestWorkflow(ctx workflow.Context, initial common.ModalEnvConfig) (modalConfigWorkflowResult, error) {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	envContainer := &env.EnvContainer{Env: &env.ModalEnv{
		WorkingDirectory: "/root/repo",
		SandboxName:      "side--repo-abc",
		SSHHost:          "old.modal.host",
		SSHPort:          1111,
		LocalRepoDir:     "/host/repo",
	}}
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:      ctx,
			GlobalState:  globalState,
			EnvContainer: envContainer,
		},
		RepoConfig: common.RepoConfig{ModalConfig: initial},
	}

	if err := SetupModalConfigHandlers(dCtx); err != nil {
		return modalConfigWorkflowResult{}, err
	}

	_ = workflow.Sleep(ctx, 100*time.Millisecond)

	modalEnv := envContainer.Env.(*env.ModalEnv)
	config, _ := globalState.GetValue(globalStateKeyModalConfig).(common.ModalEnvConfig)
	return modalConfigWorkflowResult{
		Config:      config,
		SandboxName: modalEnv.SandboxName,
		SSHHost:     modalEnv.SSHHost,
		SSHPort:     modalEnv.SSHPort,
	}, nil
}

func TestModalConfigUpdateRecreatesSandboxAndRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	var workflowSuite testsuite.WorkflowTestSuite
	wfEnv := workflowSuite.NewTestWorkflowEnvironment()
	wfEnv.SetWorkerOptions(utils.TestWorkerOptions())
	defer wfEnv.AssertExpectations(t)

	initial := common.ModalEnvConfig{Memory: 1024}
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
	}}
	wfEnv.OnActivity(env.ModalRecreateSandboxActivity, mock.Anything, mock.MatchedBy(func(input env.ModalRecreateSandboxInput) bool {
		return input.Config.Memory == validUpdate.Memory && input.Config.MemoryLimit == validUpdate.MemoryLimit
	})).Return(env.ModalRecreateSandboxOutput{EnvContainer: recreatedEnvContainer}, nil).Once()

	invalidCallbacks := &modelConfigUpdateCallbacks{}
	validCallbacks := &modelConfigUpdateCallbacks{}
	var queriedConfig common.ModalEnvConfig
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
	}, 50*time.Millisecond)

	wfEnv.ExecuteWorkflow(modalConfigTestWorkflow, initial)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())

	// The invalid configuration must be rejected by the validator, before the
	// sandbox recreation activity runs.
	require.False(t, invalidCallbacks.accepted)
	require.Error(t, invalidCallbacks.rejection)
	require.Contains(t, invalidCallbacks.rejection.Error(), "configured more than once")

	require.True(t, validCallbacks.accepted)
	require.NoError(t, validCallbacks.rejection)
	require.True(t, validCallbacks.completed)
	require.NoError(t, validCallbacks.err)

	require.Equal(t, validUpdate, queriedConfig)
	var result modalConfigWorkflowResult
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	require.Equal(t, validUpdate, result.Config)
	require.Equal(t, "side--repo-abc", result.SandboxName)
	require.Equal(t, "new.modal.host", result.SSHHost)
	require.Equal(t, 2222, result.SSHPort)
}
