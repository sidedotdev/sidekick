package dev

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
)

type DevAgentManagerWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	ima *DevAgentManagerActivities
}

func (s *DevAgentManagerWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.ima = nil
}

func (s *DevAgentManagerWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// executeWorkRequest must run inside a workflow, so tests drive it through a
// thin wrapper workflow that returns its result.
func (s *DevAgentManagerWorkflowTestSuite) runWorkRequest(req WorkRequest) (domain.Flow, error) {
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, workRequest WorkRequest) (domain.Flow, error) {
			ctx = setActivityOptions(ctx)
			var ima *DevAgentManagerActivities
			return executeWorkRequest(ctx, "ws_123", workRequest, ima)
		},
		workflow.RegisterOptions{Name: "wrapExecuteWorkRequest"},
	)
	s.env.ExecuteWorkflow("wrapExecuteWorkRequest", req)
	s.True(s.env.IsWorkflowCompleted())

	var flow domain.Flow
	err := s.env.GetWorkflowResult(&flow)
	return flow, err
}

func (s *DevAgentManagerWorkflowTestSuite) TestExecuteWorkRequest_IddDispatchAndTitleMapping() {
	var captured IddWorkflowInput
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input IddWorkflowInput) error {
			captured = input
			return nil
		},
		workflow.RegisterOptions{Name: "IddWorkflow"},
	)
	s.env.OnActivity(s.ima.FindWorkspaceById, mock.Anything, "ws_123").Return(domain.Workspace{
		Id:           "ws_123",
		LocalRepoDir: "/tmp/test-repo",
	}, nil)
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.AnythingOfType("domain.Flow")).Return(nil)

	flow, err := s.runWorkRequest(WorkRequest{
		ParentId:    "task_456",
		FlowType:    "idd",
		FlowOptions: map[string]interface{}{},
		Title:       "Build the intent canvas",
	})

	s.NoError(err)
	s.Equal(domain.FlowType("idd"), flow.Type)
	s.Equal("Build the intent canvas", captured.Title)
	s.Equal("ws_123", captured.WorkspaceId)
}

func (s *DevAgentManagerWorkflowTestSuite) TestExecuteWorkRequest_IddRequiresTitle() {
	s.env.OnActivity(s.ima.FindWorkspaceById, mock.Anything, "ws_123").Return(domain.Workspace{
		Id:           "ws_123",
		LocalRepoDir: "/tmp/test-repo",
	}, nil)

	_, err := s.runWorkRequest(WorkRequest{
		ParentId:    "task_456",
		FlowType:    "idd",
		FlowOptions: map[string]interface{}{},
		Title:       "   ",
	})

	s.Error(err)
	s.Contains(err.Error(), "title is required")
}

func TestDevAgentManagerWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DevAgentManagerWorkflowTestSuite))
}
