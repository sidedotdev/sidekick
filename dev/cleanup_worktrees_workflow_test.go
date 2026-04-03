package dev

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

type CleanupWorktreesWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *CleanupWorktreesWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *CleanupWorktreesWorkflowTestSuite) TestCleansUpAllWorkspaces() {
	s.env.OnActivity(
		(&DevAgentManagerActivities{}).ListWorkspaces,
		mock.Anything,
	).Return(ListWorkspacesResult{
		WorkspaceIds: []string{"ws_1", "ws_2"},
	}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_1", DryRun: false},
	).Return(CleanupStaleWorktreesReport{Candidates: []StaleWorktreeCandidate{{Path: "/tmp/a"}}}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_2", DryRun: false},
	).Return(CleanupStaleWorktreesReport{}, nil)

	s.env.ExecuteWorkflow(CleanupWorktreesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *CleanupWorktreesWorkflowTestSuite) TestContinuesOnCleanupError() {
	s.env.OnActivity(
		(&DevAgentManagerActivities{}).ListWorkspaces,
		mock.Anything,
	).Return(ListWorkspacesResult{
		WorkspaceIds: []string{"ws_fail", "ws_ok"},
	}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_fail", DryRun: false},
	).Return(CleanupStaleWorktreesReport{}, fmt.Errorf("cleanup failed"))

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_ok", DryRun: false},
	).Return(CleanupStaleWorktreesReport{}, nil)

	s.env.ExecuteWorkflow(CleanupWorktreesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *CleanupWorktreesWorkflowTestSuite) TestNoWorkspaces() {
	s.env.OnActivity(
		(&DevAgentManagerActivities{}).ListWorkspaces,
		mock.Anything,
	).Return(ListWorkspacesResult{WorkspaceIds: nil}, nil)

	s.env.ExecuteWorkflow(CleanupWorktreesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestCleanupWorktreesWorkflow(t *testing.T) {
	suite.Run(t, new(CleanupWorktreesWorkflowTestSuite))
}
