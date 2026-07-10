package dev

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/flow_action"
	"sidekick/utils"
)

// IddPlanAutoApproveTestSuite verifies that plans built for IDD sub-tasks are
// approved automatically, without a human-in-the-loop request.
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
	testWorkflow := func(ctx workflow.Context) (*flow_action.UserResponse, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
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
}

func TestIddPlanAutoApproveTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(IddPlanAutoApproveTestSuite))
}