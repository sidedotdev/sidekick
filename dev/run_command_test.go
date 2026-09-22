package dev

import (
	"context"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRunCommandDispatchesRunCommandHooks(t *testing.T) {
	t.Parallel()
	hooks := common.HooksConfig{RunCommand: []common.HookRuleConfig{{
		Match:   &common.HookMatchConfig{EnvTypes: []string{"modal"}},
		Actions: []common.HookActionConfig{{Type: common.HookActionScript, Source: "emitCommandEnvironment({envVars: {A: '1'}})"}},
	}}}
	for _, tc := range []struct {
		name      string
		devEnv    env.Env
		wantHooks bool
	}{
		{name: "matching env type", devEnv: &env.ModalEnv{WorkingDirectory: "/workspace", SandboxName: "sb-1"}, wantHooks: true},
		{name: "non-matching env type", devEnv: &env.LocalEnv{WorkingDirectory: t.TempDir()}, wantHooks: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			te := suite.NewTestWorkflowEnvironment()
			te.SetWorkerOptions(utils.TestWorkerOptions())

			var fa *flow_action.FlowActivities
			te.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			te.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{Result: common.PermissionAutoApprove}, nil).Once()

			var input env.EnvRunCommandActivityInput
			te.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(ctx context.Context, in env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					input = in
					return env.EnvRunCommandActivityOutput{Stdout: "ok", ExitStatus: 0}, nil
				},
			).Once()

			envContainer := env.EnvContainer{Env: tc.devEnv}
			te.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				dCtx := DevContext{
					ExecContext: flow_action.ExecContext{
						Context:      utils.NoRetryCtx(ctx),
						WorkspaceId:  "ws-run-command",
						EnvContainer: &envContainer,
						GlobalState:  &flow_action.GlobalState{},
						FlowScope:    &flow_action.FlowScope{SubflowName: "RunCommandTest"},
					},
					RepoConfig: common.RepoConfig{Hooks: hooks},
				}
				return RunCommand(dCtx, RunCommandParams{Command: "echo hi", WorkingDir: "."})
			})
			require.True(t, te.IsWorkflowCompleted())
			require.NoError(t, te.GetWorkflowError())
			te.AssertExpectations(t)

			require.Equal(t, "sh", input.Command)
			require.Equal(t, []string{"-c", "echo hi"}, input.Args)
			if !tc.wantHooks {
				require.Empty(t, input.Hooks)
				require.Equal(t, env.CommandHookContext{}, input.HookContext)
				return
			}
			require.Len(t, input.Hooks, 1)
			require.Equal(t, "hooks.run_command[0].actions[0]", input.Hooks[0].Location)
			require.Equal(t, common.HookActionScript, input.Hooks[0].Type)
			require.Equal(t, "emitCommandEnvironment({envVars: {A: '1'}})", input.Hooks[0].Source)
			require.Equal(t, "ws-run-command", input.HookContext.WorkspaceId)
			require.Equal(t, hooks, input.HookContext.RepoConfig.Hooks)
		})
	}
}
