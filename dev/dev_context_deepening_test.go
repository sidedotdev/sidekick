package dev

import (
	"errors"
	"sidekick/env"
	"sidekick/utils"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestScheduleRepoDeepeningSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		environment  env.Env
		deepenOutput env.DeepenRepoOutput
		deepenErr    error
		wantSnapshot bool
	}{
		{
			name:         "snapshots deepened Modal repo",
			environment:  &env.ModalEnv{SandboxName: "sandbox", WorkingDirectory: "/repo"},
			deepenOutput: env.DeepenRepoOutput{Deepened: true},
			wantSnapshot: true,
		},
		{
			name:         "does not snapshot complete Modal repo",
			environment:  &env.ModalEnv{SandboxName: "sandbox", WorkingDirectory: "/repo"},
			deepenOutput: env.DeepenRepoOutput{Deepened: false},
		},
		{
			name:         "does not snapshot non-Modal repo",
			environment:  &env.LocalEnv{WorkingDirectory: "/repo"},
			deepenOutput: env.DeepenRepoOutput{Deepened: true},
		},
		{
			name:        "does not snapshot after deepening failure",
			environment: &env.ModalEnv{SandboxName: "sandbox", WorkingDirectory: "/repo"},
			deepenErr:   errors.New("deepening failed"),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var testSuite testsuite.WorkflowTestSuite
			testEnv := testSuite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())

			envContainer := env.EnvContainer{Env: tt.environment}
			deepenInput := env.DeepenRepoInput{
				EnvContainer:  envContainer,
				LocalRepoDir:  "/local/repo",
				RemoteRepoDir: "/repo",
				Branches:      []string{"main"},
			}
			snapshotInput := env.SnapshotEnvironmentInput{
				EnvContainer:  envContainer,
				RemoteRepoDir: "/repo",
			}

			testEnv.OnActivity(env.DeepenRepoActivity, mock.Anything, deepenInput).
				Return(tt.deepenOutput, tt.deepenErr).
				Once()
			if tt.wantSnapshot {
				testEnv.OnActivity(env.SnapshotEnvironmentActivity, mock.Anything, snapshotInput).
					Return(env.SnapshotEnvironmentOutput{Snapshotted: true, Attempts: 1}, nil).
					Once()
			}

			wrapperWorkflow := func(ctx workflow.Context) error {
				scheduleRepoDeepening(ctx, envContainer, "/local/repo", "/repo", []string{"main"})
				return workflow.Sleep(ctx, time.Second)
			}
			testEnv.ExecuteWorkflow(wrapperWorkflow)

			require.True(t, testEnv.IsWorkflowCompleted())
			require.NoError(t, testEnv.GetWorkflowError())
			testEnv.AssertExpectations(t)
		})
	}
}
