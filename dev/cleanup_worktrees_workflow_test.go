package dev

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"

	"sidekick/utils"
)

type CleanupWorktreesWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *CleanupWorktreesWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
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

func (s *CleanupWorktreesWorkflowTestSuite) TestSkipsUnhealthyHibernationCandidates() {
	s.env.OnActivity(
		(&DevAgentManagerActivities{}).ListWorkspaces,
		mock.Anything,
	).Return(ListWorkspacesResult{WorkspaceIds: []string{"ws_1"}}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_1", DryRun: false},
	).Return(CleanupStaleWorktreesReport{}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).FindHibernationCandidates,
		mock.Anything,
		mock.Anything,
	).Return(HibernationCandidatesOutput{Candidates: []HibernationCandidate{
		{FlowId: "flow_healthy"},
		{FlowId: "flow_stuck"},
	}}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CheckWorkflowHealth,
		mock.Anything,
		WorkflowHealthInput{WorkflowId: "flow_healthy"},
	).Return(WorkflowHealthOutput{PendingWorkflowTaskAttempt: 1}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CheckWorkflowHealth,
		mock.Anything,
		WorkflowHealthInput{WorkflowId: "flow_stuck"},
	).Return(WorkflowHealthOutput{
		PendingWorkflowTaskAttempt: 27,
		PendingWorkflowTaskAge:     time.Hour,
	}, nil)

	var signaled []string
	s.env.OnSignalExternalWorkflow(
		mock.Anything, mock.Anything, mock.Anything, SignalNameHibernate, mock.Anything,
	).Return(nil).Run(func(args mock.Arguments) {
		signaled = append(signaled, args.Get(1).(string))
	})

	s.env.ExecuteWorkflow(CleanupWorktreesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal([]string{"flow_healthy"}, signaled)
	s.env.AssertExpectations(s.T())
}

func (s *CleanupWorktreesWorkflowTestSuite) TestHibernatesWhenHealthCheckFails() {
	s.env.OnActivity(
		(&DevAgentManagerActivities{}).ListWorkspaces,
		mock.Anything,
	).Return(ListWorkspacesResult{WorkspaceIds: []string{"ws_1"}}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CleanupStaleWorktrees,
		mock.Anything,
		CleanupStaleWorktreesInput{WorkspaceId: "ws_1", DryRun: false},
	).Return(CleanupStaleWorktreesReport{}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).FindHibernationCandidates,
		mock.Anything,
		mock.Anything,
	).Return(HibernationCandidatesOutput{Candidates: []HibernationCandidate{
		{FlowId: "flow_1"},
	}}, nil)

	s.env.OnActivity(
		(&DevAgentManagerActivities{}).CheckWorkflowHealth,
		mock.Anything,
		WorkflowHealthInput{WorkflowId: "flow_1"},
	).Return(WorkflowHealthOutput{}, fmt.Errorf("describe failed"))

	var signaled []string
	s.env.OnSignalExternalWorkflow(
		mock.Anything, mock.Anything, mock.Anything, SignalNameHibernate, mock.Anything,
	).Return(nil).Run(func(args mock.Arguments) {
		signaled = append(signaled, args.Get(1).(string))
	})

	s.env.ExecuteWorkflow(CleanupWorktreesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal([]string{"flow_1"}, signaled)
	s.env.AssertExpectations(s.T())
}

func TestCleanupWorktreesWorkflow(t *testing.T) {
	suite.Run(t, new(CleanupWorktreesWorkflowTestSuite))
}
