package dev

import (
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestAppendWebSearchToolIfNonLocal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		envContainer *env.EnvContainer
		wantAdded    bool
	}{
		{
			name:         "local env stays disabled",
			envContainer: &env.EnvContainer{Env: &env.LocalEnv{}},
			wantAdded:    false,
		},
		{
			name:         "local git worktree env stays disabled",
			envContainer: &env.EnvContainer{Env: &env.LocalGitWorktreeEnv{}},
			wantAdded:    false,
		},
		{
			name:         "devpod env enables web search",
			envContainer: &env.EnvContainer{Env: &env.DevPodEnv{}},
			wantAdded:    true,
		},
		{
			name:         "open shell env enables web search",
			envContainer: &env.EnvContainer{Env: &env.OpenShellEnv{}},
			wantAdded:    true,
		},
		{
			name:         "missing env stays disabled",
			envContainer: &env.EnvContainer{},
			wantAdded:    false,
		},
		{
			name:         "missing env container stays disabled",
			envContainer: nil,
			wantAdded:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var ts testsuite.WorkflowTestSuite
			wfEnv := ts.NewTestWorkflowEnvironment()

			existing := &llm.Tool{Name: "some_function_tool"}
			wf := func(ctx workflow.Context) ([]*llm.Tool, error) {
				dCtx := DevContext{
					ExecContext: flow_action.ExecContext{
						Context:      ctx,
						EnvContainer: tc.envContainer,
					},
				}
				return appendWebSearchToolIfNonLocal(dCtx, []*llm.Tool{existing}), nil
			}
			wfEnv.RegisterWorkflow(wf)
			wfEnv.ExecuteWorkflow(wf)

			require.True(t, wfEnv.IsWorkflowCompleted())
			require.NoError(t, wfEnv.GetWorkflowError())

			var tools []*llm.Tool
			require.NoError(t, wfEnv.GetWorkflowResult(&tools))

			require.NotEmpty(t, tools)
			assert.Equal(t, "some_function_tool", tools[0].Name)
			if tc.wantAdded {
				require.Len(t, tools, 2)
				assert.Equal(t, common.ToolTypeWebSearch, tools[1].Type)
			} else {
				require.Len(t, tools, 1)
			}
		})
	}
}