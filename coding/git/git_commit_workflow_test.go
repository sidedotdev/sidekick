package git

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

type GitCommitWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *GitCommitWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *GitCommitWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *GitCommitWorkflowTestSuite) commitWorkflow(ctx workflow.Context) error {
	ctx = utils.NoRetryCtx(ctx)
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	eCtx := flow_action.ExecContext{
		WorkspaceId: "test-workspace",
		Context:     ctx,
		FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
		GlobalState: gs,
		EnvContainer: &env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
		},
	}
	return GitCommit(eCtx, "some commit message")
}

// TestGitCommitToleratesNothingToCommit ensures an empty commit stays a no-op
// instead of being routed into the user-controlled retry mechanism, which would
// otherwise block the flow waiting for a human to retry an expected outcome.
// The activity itself is asked to tolerate it, so it never fails in the first
// place (see TestGitCommitActivity_IgnoreNothingToCommit).
func (s *GitCommitWorkflowTestSuite) TestGitCommitToleratesNothingToCommit() {
	s.env.OnActivity(GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("some diff", nil).Once()

	commitCalls := 0
	s.env.OnActivity(GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params GitCommitParams) (string, error) {
			commitCalls++
			if !params.IgnoreNothingToCommit {
				return "", errors.New("git commit failed: nothing to commit, working tree clean")
			}
			return "nothing to commit, working tree clean", nil
		},
	)

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.RegisterWorkflow(s.commitWorkflow)
	s.env.ExecuteWorkflow(s.commitWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(1, commitCalls, "a nothing-to-commit result should neither fail nor prompt for retry")
}

// TestGitCommitSurfacesRealFailure ensures genuine commit failures (including
// flow-branch backup sync failures) are not swallowed.
func (s *GitCommitWorkflowTestSuite) TestGitCommitSurfacesRealFailure() {
	s.env.OnActivity(GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("some diff", nil).Once()
	s.env.OnActivity(GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		"", errors.New("commit succeeded but failed to sync flow branch to local repo"),
	).Once()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	failFast := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		eCtx := flow_action.ExecContext{
			WorkspaceId: "test-workspace",
			Context:     ctx,
			FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
			GlobalState: gs,
			EnvContainer: &env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
			},
			DisableHumanInTheLoop: true,
		}
		return GitCommit(eCtx, "some commit message")
	}
	s.env.RegisterWorkflow(failFast)
	s.env.ExecuteWorkflow(failFast)

	s.True(s.env.IsWorkflowCompleted())
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "failed to sync flow branch to local repo")
}

func TestGitCommitWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(GitCommitWorkflowTestSuite))
}
