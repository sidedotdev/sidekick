package dev

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
)

// CriteriaFulfillmentDiffTestSuite covers which diff the auto-reviewer judges:
// the diff since the last review once there is one, the full diff otherwise,
// and a graceful full-diff fallback when git itself fails.
type CriteriaFulfillmentDiffTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *CriteriaFulfillmentDiffTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *CriteriaFulfillmentDiffTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *CriteriaFulfillmentDiffTestSuite) newDevContext(ctx workflow.Context) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	return DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId: "test-workspace",
			Context:     ctx,
			FlowScope: &flow_action.FlowScope{
				SubflowName: "test-subflow",
			},
			GlobalState: gs,
			EnvContainer: &env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
			},
		},
		RepoConfig: common.RepoConfig{},
	}
}

// runDiffWorkflow executes criteriaFulfillmentReviewDiff in a workflow, also
// reporting whether any user prompt was issued along the way.
func (s *CriteriaFulfillmentDiffTestSuite) runDiffWorkflow(promptInfo CheckWorkInfo) (string, bool, error) {
	requestForUserSeen := false
	testWorkflow := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			requestForUserSeen = true
		})

		return criteriaFulfillmentReviewDiff(s.newDevContext(ctx), promptInfo, true)
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())

	if err := s.env.GetWorkflowError(); err != nil {
		return "", requestForUserSeen, err
	}
	var diff string
	s.Require().NoError(s.env.GetWorkflowResult(&diff))
	return diff, requestForUserSeen, nil
}

func (s *CriteriaFulfillmentDiffTestSuite) TestUsesSinceDiffWhenPriorReviewExists() {
	var params []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			params = append(params, p)
			return coding.GenerateReviewDiffsResult{FullDiff: "full diff", SinceDiff: "since diff"}, nil
		},
	)

	diff, requestForUserSeen, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		StartPoint:     "pinned-sha",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("since diff", diff)
	s.False(requestForUserSeen)

	s.Require().Len(params, 1)
	s.Equal("pinned-sha", params[0].StartPoint)
	s.Equal("main", params[0].BaseBranch)
	s.Equal("prior review diff", params[0].PriorReviewDiff)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestUsesFullDiffOnFirstRound() {
	var params []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			params = append(params, p)
			return coding.GenerateReviewDiffsResult{FullDiff: "full diff"}, nil
		},
	)

	diff, _, err := s.runDiffWorkflow(CheckWorkInfo{BaseBranch: "main"})
	s.Require().NoError(err)
	s.Equal("full diff", diff)

	s.Require().Len(params, 1)
	s.Equal("main", params[0].StartPoint, "the base branch is the start point when none is pinned")
	s.Empty(params[0].PriorReviewDiff)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestFallsBackToFullDiffWhenSinceDiffDegrades() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		coding.GenerateReviewDiffsResult{
			FullDiff:       "full diff",
			SinceDiffError: "unparseable prior diff",
		}, nil,
	)

	diff, _, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("full diff", diff)
}

// A git failure can't be fixed by retrying at the user's prompt, so the check
// falls back to the base-branch diff instead of blocking on the user.
func (s *CriteriaFulfillmentDiffTestSuite) TestFallsBackToBaseBranchDiffWithoutUserPromptOnActivityFailure() {
	callCount := 0
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			callCount++
			return coding.GenerateReviewDiffsResult{}, errors.New("bad object deadbeef")
		},
	)
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("fallback diff", nil)

	diff, requestForUserSeen, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("fallback diff", diff)
	s.False(requestForUserSeen, "an unresolvable review-diff failure should not prompt the user")
	s.Equal(1, callCount, "the activity should not be retried via user prompt")
}

func TestCriteriaFulfillmentDiffTestSuite(t *testing.T) {
	suite.Run(t, new(CriteriaFulfillmentDiffTestSuite))
}
