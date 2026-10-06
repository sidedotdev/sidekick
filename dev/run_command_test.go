package dev

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRunCommandPassesGitIdentityEnvVars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		globalState     map[string]string
		nilGlobalState  bool
		expectedEnvVars []string
	}{
		{
			name: "committer from global state",
			globalState: map[string]string{
				"committerName":  "Dev User",
				"committerEmail": "dev@example.com",
			},
			expectedEnvVars: []string{
				"GIT_AUTHOR_NAME=Sidekick",
				"GIT_AUTHOR_EMAIL=sidekick@side.dev",
				"GIT_COMMITTER_NAME=Dev User",
				"GIT_COMMITTER_EMAIL=dev@example.com",
			},
		},
		{
			name:        "committer missing from global state",
			globalState: map[string]string{},
			expectedEnvVars: []string{
				"GIT_AUTHOR_NAME=Sidekick",
				"GIT_AUTHOR_EMAIL=sidekick@side.dev",
			},
		},
		{
			name:           "nil global state",
			nilGlobalState: true,
			expectedEnvVars: []string{
				"GIT_AUTHOR_NAME=Sidekick",
				"GIT_AUTHOR_EMAIL=sidekick@side.dev",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var workflowSuite testsuite.WorkflowTestSuite
			testEnv := workflowSuite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())

			devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: t.TempDir()})
			require.NoError(t, err)
			envContainer := env.EnvContainer{Env: devEnv}

			var globalState *flow_action.GlobalState
			if !tt.nilGlobalState {
				globalState = &flow_action.GlobalState{}
				for k, v := range tt.globalState {
					globalState.SetValue(k, v)
				}
			}

			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).Return(
				CheckCommandPermissionOutput{Result: common.PermissionAutoApprove}, nil,
			).Once()

			var captured env.EnvRunCommandActivityInput
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(ctx context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					captured = input
					return env.EnvRunCommandActivityOutput{Stdout: "ok", ExitStatus: 0}, nil
				},
			).Once()

			wrapperWorkflow := func(ctx workflow.Context) (string, error) {
				dCtx := DevContext{
					ExecContext: flow_action.ExecContext{
						Context:      utils.NoRetryCtx(ctx),
						EnvContainer: &envContainer,
						GlobalState:  globalState,
					},
				}
				return RunCommand(dCtx, RunCommandParams{Command: "git commit -m test", WorkingDir: "sub"})
			}
			testEnv.RegisterWorkflow(wrapperWorkflow)

			testEnv.ExecuteWorkflow(wrapperWorkflow)
			require.True(t, testEnv.IsWorkflowCompleted())
			require.NoError(t, testEnv.GetWorkflowError())
			testEnv.AssertExpectations(t)

			var result string
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			require.Contains(t, result, "Command executed with exit status 0")

			require.Equal(t, "sh", captured.Command)
			require.Equal(t, []string{"-c", "git commit -m test"}, captured.Args)
			require.Equal(t, "sub", captured.RelativeWorkingDir)
			require.Equal(t, tt.expectedEnvVars, captured.EnvVars)
		})
	}
}
