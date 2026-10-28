package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"
	"sidekick/env"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestEnvSwitchSavesSourceWIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		target           env.EnvType
		restoreFails     bool
		cleanupFails     bool
		retryableCleanup bool
		legacy           bool
	}{
		{name: "remote target", target: env.EnvTypeModal},
		{name: "shared host checkout", target: env.EnvTypeDevPod},
		{name: "failed restore", target: env.EnvTypeModal, restoreFails: true},
		{name: "failed cleanup", target: env.EnvTypeModal, cleanupFails: true},
		{name: "retryable cleanup failure", target: env.EnvTypeModal, retryableCleanup: true},
		{name: "legacy history", target: env.EnvTypeModal, legacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvGateTestEnv(t)
			source := switchTestLocalEnv()
			recorder := &envSwitchRecorder{}
			restored := false
			if tt.target == env.EnvTypeModal {
				mockSwitchToModal(wfEnv, recorder, nil).
					Return(func(context.Context, env.RestoreFromSwitchInput) (env.RestoreFromSwitchOutput, error) {
						if tt.restoreFails {
							return env.RestoreFromSwitchOutput{}, temporal.NewNonRetryableApplicationError("restore failed", "test", nil)
						}
						restored = true
						return env.RestoreFromSwitchOutput{}, nil
					})
			} else {
				mockSwitchBookkeeping(wfEnv, recorder)
				target := mockSwitchDevPodProvisioning(wfEnv)
				gate := make(chan struct{})
				close(gate)
				mockSwitchTransfer(wfEnv, recorder, source, target, gate, nil)
			}
			if tt.legacy {
				wfEnv.OnGetVersion("env-switch-save-source-wip", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Once()
			}
			var saved []env.SaveEnvSwitchWIPInput
			cleanupBeforeRestore := false
			wfEnv.OnActivity(env.SaveEnvSwitchWIPActivity, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input env.SaveEnvSwitchWIPInput) (env.SaveEnvSwitchWIPOutput, error) {
					cleanupBeforeRestore = !restored
					saved = append(saved, input)
					if tt.retryableCleanup {
						return env.SaveEnvSwitchWIPOutput{}, temporal.NewApplicationError("cleanup failed", "test")
					}
					if tt.cleanupFails {
						return env.SaveEnvSwitchWIPOutput{}, temporal.NewNonRetryableApplicationError("cleanup failed", "test", nil)
					}
					return env.SaveEnvSwitchWIPOutput{}, nil
				}).Maybe()
			callbacks := &envUpdateCallbacks{}
			wfEnv.RegisterDelayedCallback(func() {
				wfEnv.UpdateWorkflow(UpdateNameEnv, "switch", callbacks, EnvSettings{EnvType: string(tt.target)})
			}, 5*time.Millisecond)
			wfEnv.ExecuteWorkflow(envSwitchTestWorkflow, envSwitchTestInput{Source: &source})
			require.NoError(t, wfEnv.GetWorkflowError())

			var result envSwitchWorkflowResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			if tt.restoreFails {
				require.Equal(t, env.IdentityOf(source), env.IdentityOf(result.EnvContainer))
				require.Equal(t, domain.ActionStatusFailed, recorder.lastSwitchAction(t).ActionStatus)
			} else {
				require.Equal(t, tt.target, result.EnvContainer.Env.GetType())
				require.Equal(t, domain.ActionStatusComplete, recorder.lastSwitchAction(t).ActionStatus)
				for _, retired := range result.Retired {
					require.NotEqual(t, env.IdentityOf(result.EnvContainer), env.IdentityOf(retired.EnvContainer))
				}
			}
			if tt.target == env.EnvTypeModal && !tt.restoreFails && !tt.legacy {
				attempts := 1
				if tt.retryableCleanup {
					attempts = 3
				}
				require.Len(t, saved, attempts)
				require.False(t, cleanupBeforeRestore)
				require.Equal(t, env.IdentityOf(source), env.IdentityOf(saved[0].EnvContainer))
				require.Equal(t, switchTestBranch, saved[0].Branch)
				require.Equal(t, switchTestHead, saved[0].HeadCommit)
			} else {
				require.Empty(t, saved)
			}
		})
	}
}
