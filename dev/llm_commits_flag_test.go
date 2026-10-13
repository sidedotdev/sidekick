package dev

import (
	"testing"

	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestLlmCommitMessagesEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version workflow.Version
		want    bool
	}{
		{name: "undefined flag defaults on", version: 1, want: true},
		{name: "off for flows predating the flag", version: workflow.DefaultVersion, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.OnGetVersion(fflag.VersionChangeId(fflag.LlmCommitMessages), workflow.DefaultVersion, 1).Return(tt.version)

			var activities *fflag.FFlagActivities
			env.OnActivity(activities.EvaluateFlags, mock.Anything, mock.Anything).
				Return(fflag.EvaluateFlagsOutput{}, nil).Maybe()

			wf := func(ctx workflow.Context) (bool, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: utils.NoRetryCtx(ctx), GlobalState: &flow_action.GlobalState{},
				}}
				return llmCommitMessagesEnabled(dCtx), nil
			}
			env.ExecuteWorkflow(wf)
			require.NoError(t, env.GetWorkflowError())
			var enabled bool
			require.NoError(t, env.GetWorkflowResult(&enabled))
			require.Equal(t, tt.want, enabled)
		})
	}
}
