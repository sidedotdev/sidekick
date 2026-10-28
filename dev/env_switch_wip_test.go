package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"
	"sidekick/env"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestEnvSwitchSavesSourceWIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		target       env.EnvType
		restoreFails bool
		legacy       bool
	}{
		{name: "remote target", target: env.EnvTypeModal},
		{name: "shared host checkout", target: env.EnvTypeDevPod},
		{name: "failed restore", target: env.EnvTypeModal, restoreFails: true},
		{name: "legacy history", target: env.EnvTypeModal, legacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvGateTestEnv(t)
			source := switchTestLocalEnv()
			recorder := &envSwitchRecorder{}
			if tt.target == env.EnvTypeModal {
				mockSwitchToModal(wfEnv, recorder, nil).
					Return(func(context.Context, env.RestoreFromSwitchInput) (env.RestoreFromSwitchOutput, error) {
						if tt.restoreFails {
							return env.RestoreFromSwitchOutput{}, temporal.NewNonRetryableApplicationError("restore failed", "test", nil)
						}
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
				wfEnv.OnGetVersion("env-switch-restore-source-wip", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Once()
			}
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
			if tt.target == env.EnvTypeModal {
				require.Len(t, recorder.restoreInputs, 1)
				input := recorder.restoreInputs[0]
				if tt.legacy {
					require.Nil(t, input.SourceEnvContainer)
				} else {
					require.NotNil(t, input.SourceEnvContainer)
					require.Equal(t, env.IdentityOf(source), env.IdentityOf(*input.SourceEnvContainer))
					require.Equal(t, switchTestBranch, input.Bundle.Branch)
					require.Equal(t, switchTestHead, input.Bundle.HeadCommit)
				}
			}
			wfEnv.AssertExpectations(t)
		})
	}
}
