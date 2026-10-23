package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
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

func TestCheckCommandPermissionSandboxBypass(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		environment env.Env
		legacy      bool
		bypass      bool
	}{
		{name: "modal", environment: &env.ModalEnv{}, bypass: true},
		{name: "openshell", environment: &env.OpenShellEnv{}, bypass: true},
		{name: "devpod", environment: &env.DevPodEnv{}, bypass: true},
		{name: "local", environment: &env.LocalEnv{}},
		{name: "local worktree", environment: &env.LocalGitWorktreeEnv{}},
		{name: "legacy modal", environment: &env.ModalEnv{}, legacy: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.RegisterActivity(CheckCommandPermissionActivity)
			if tt.legacy {
				testEnv.OnGetVersion("sandbox-command-permissions-bypass", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Maybe()
			}
			permissionCall := testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{
					Result:  common.PermissionDeny,
					Message: "Configured denial",
				}, nil)
			if tt.bypass {
				permissionCall.Maybe()
			} else {
				permissionCall.Once()
			}

			testWorkflow := func(ctx workflow.Context) ([]string, error) {
				ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
					StartToCloseTimeout: time.Minute,
				})
				dCtx := DevContext{
					ExecContext: flow_action.ExecContext{
						Context:      ctx,
						EnvContainer: &env.EnvContainer{Env: tt.environment},
					},
					RepoConfig: common.RepoConfig{
						CommandPermissions: common.CommandPermissionConfig{
							Deny: []common.CommandPattern{{Pattern: "dd if=", Message: "Configured denial"}},
						},
					},
				}
				proceed, denial, advisory, err := checkCommandPermission(dCtx,
					"dd if=/dev/zero of=/swapfile bs=1M count=4096", ".")
				if err != nil {
					return nil, err
				}
				outcome := "blocked"
				if proceed {
					outcome = "proceed"
				}
				return []string{outcome, denial, advisory}, nil
			}
			testEnv.ExecuteWorkflow(testWorkflow)
			require.True(t, testEnv.IsWorkflowCompleted())
			require.NoError(t, testEnv.GetWorkflowError())
			var result []string
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			if tt.bypass {
				assert.Equal(t, []string{"proceed", "", ""}, result)
				testEnv.AssertNotCalled(t, "CheckCommandPermissionActivity", mock.Anything, mock.Anything)
			} else {
				assert.Equal(t, []string{"blocked", "Command denied: Configured denial", ""}, result)
			}
			testEnv.AssertExpectations(t)
		})
	}
}
