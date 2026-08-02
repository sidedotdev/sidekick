package dev

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/temporalmeta"
	"sidekick/utils"
)

// IddPlanAutoApproveTestSuite verifies that plans built for IDD sub-tasks are
// approved automatically, without blocking for human input, while still
// recording a completed flow action.
type IddPlanAutoApproveTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *IddPlanAutoApproveTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

// TestIddAutoApprovesPlan ensures ApproveDevPlan approves immediately for IDD
// sub-tasks. The workflow completes without blocking, which would not happen
// if a human plan approval request were issued.
func (s *IddPlanAutoApproveTestSuite) TestIddAutoApprovesPlan() {
	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	testWorkflow := func(ctx workflow.Context) (*flow_action.UserResponse, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
			},
			Idd: true,
		}
		return ApproveDevPlan(dCtx, DevPlan{})
	}
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *flow_action.UserResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Require().NotNil(result.Approved)
	s.True(*result.Approved)

	// The auto-approval must still be recorded as a completed, non-human flow action
	s.Require().NotEmpty(persistedActions)
	last := persistedActions[len(persistedActions)-1]
	s.Equal("user_request.approve.dev_plan", last.ActionType)
	s.Equal(domain.ActionStatusComplete, last.ActionStatus)
	s.False(last.IsHumanAction)
	s.Contains(last.ActionResult, `"Approved":true`)
}

func TestIddPlanAutoApproveTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(IddPlanAutoApproveTestSuite))
}
