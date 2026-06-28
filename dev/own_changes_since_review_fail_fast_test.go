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
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
)

// OwnChangesSinceReviewFailFastTestSuite verifies that getOwnChangesSinceReview
// surfaces activity failures directly instead of trapping the user in an
// unresolvable user-continue retry loop. A stale review tree hash (e.g. a "bad
// object" after history rewrites) can never be fixed by retrying, so callers
// must receive the error and recover gracefully: the merge-approval path shows
// the error in place of the diff, while auto-review falls back to the full
// base-branch diff.
type OwnChangesSinceReviewFailFastTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *OwnChangesSinceReviewFailFastTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *OwnChangesSinceReviewFailFastTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *OwnChangesSinceReviewFailFastTestSuite) newDevContext(ctx workflow.Context) DevContext {
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

// TestFailsFastWithoutUserPrompt ensures that when GetOwnChangesSinceReviewActivity
// fails, the error is returned without issuing a user-continue prompt, even with
// human-in-the-loop enabled. This is what makes the auto-review fallback to the
// full base-branch diff reachable instead of the user being stuck retrying an
// error that can never succeed.
func (s *OwnChangesSinceReviewFailFastTestSuite) TestFailsFastWithoutUserPrompt() {
	callCount := 0
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GetOwnChangesSinceReviewActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params coding.GetOwnChangesSinceReviewParams) (string, error) {
			callCount++
			return "", errors.New("bad object 68c1ae705c36bc72d416c09887976046b84cf9be")
		},
	)

	requestForUserSeen := false
	testWorkflow := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			requestForUserSeen = true
		})

		dCtx := s.newDevContext(ctx)
		return getOwnChangesSinceReview(dCtx, "main", "deadbeef", false)
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "bad object")
	s.False(requestForUserSeen, "no user-continue prompt should be issued for an unresolvable since-review diff failure")
	s.Equal(1, callCount, "the activity should not be retried via user prompt")
}

func TestOwnChangesSinceReviewFailFastTestSuite(t *testing.T) {
	suite.Run(t, new(OwnChangesSinceReviewFailFastTestSuite))
}
