package dev

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/temporalmeta"
	"sidekick/utils"
)

func TestApprovalCopyContent(t *testing.T) {
	t.Parallel()

	requirements := DevRequirements{Overview: "Preserve `markdown`.\n\nA second paragraph."}
	plan := DevPlan{Steps: []DevStep{{
		StepNumber: "1",
		Title:      "Implement the feature",
		Definition: "Preserve **markdown**.\n\nInclude `code` references.",
	}}}
	for _, tc := range []struct {
		name    string
		idd     bool
		content string
		approve func(DevContext) (*flow_action.UserResponse, error)
	}{
		{
			name:    "requirements",
			content: requirements.String(),
			approve: func(ctx DevContext) (*flow_action.UserResponse, error) {
				return ApproveDevRequirements(ctx, requirements)
			},
		},
		{
			name:    "plan",
			content: plan.String(),
			approve: func(ctx DevContext) (*flow_action.UserResponse, error) {
				return ApproveDevPlan(ctx, plan)
			},
		},
		{
			name:    "idd_plan",
			idd:     true,
			content: plan.String(),
			approve: func(ctx DevContext) (*flow_action.UserResponse, error) {
				return ApproveDevPlan(ctx, plan)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			var actions []domain.FlowAction
			var fa *flow_action.FlowActivities
			env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					actions = append(actions, args.Get(1).(domain.FlowAction))
				}).Return(nil)
			var meta *temporalmeta.TemporalMetaActivities
			env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
				Return([]domain.TemporalActivityRef{}, nil).Maybe()

			testWorkflow := func(ctx workflow.Context) (*flow_action.UserResponse, error) {
				return tc.approve(DevContext{
					ExecContext: flow_action.ExecContext{
						Context:               ctx,
						WorkspaceId:           "test-workspace",
						DisableHumanInTheLoop: true,
						FlowScope:             &flow_action.FlowScope{SubflowName: "test"},
					},
					Idd: tc.idd,
				})
			}
			env.ExecuteWorkflow(testWorkflow)

			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			require.NotEmpty(t, actions)
			action := actions[len(actions)-1]
			require.Equal(t, tc.content, action.ActionParams["copyContent"])
			require.Contains(t, action.ActionParams["requestContent"], tc.content)
			require.NotEqual(t, tc.content, action.ActionParams["requestContent"])
			require.Equal(t, "approve_plan", action.ActionParams["approveTag"])
			require.Equal(t, "reject_plan", action.ActionParams["rejectTag"])
		})
	}
}
