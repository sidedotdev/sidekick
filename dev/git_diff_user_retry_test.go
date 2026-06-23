package dev

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
)

// GitDiffUserRetryTestSuite verifies that the current-version diff helpers in
// the dev workflow surface activity failures through the user-continue retry
// mechanism instead of failing fatally, and that DisableHumanInTheLoop still
// fails fast.
type GitDiffUserRetryTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *GitDiffUserRetryTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *GitDiffUserRetryTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *GitDiffUserRetryTestSuite) newDevContext(ctx workflow.Context, disableHumanInTheLoop bool) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	return DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId:           "test-workspace",
			Context:               ctx,
			DisableHumanInTheLoop: disableHumanInTheLoop,
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

// TestGetGitDiffRetriesOnActivityFailure ensures that when GitDiffActivity
// fails on its first invocation, the user-continue prompt is issued and the
// activity is retried, ultimately succeeding without fatally failing the
// workflow.
func (s *GitDiffUserRetryTestSuite) TestGetGitDiffRetriesOnActivityFailure() {
	callCount := 0
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			callCount++
			if callCount == 1 {
				return "", errors.New("simulated diff failure")
			}
			return "diff content", nil
		},
	)

	childWorkflow := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := s.newDevContext(ctx, false)
		return GetGitDiff(dCtx, "main", false)
	}

	parentWorkflow := func(ctx workflow.Context) (string, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var req flow_action.RequestForUser
				signalCh.Receive(ctx, &req)
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-get-git-diff",
		})
		var diff string
		err := workflow.ExecuteChildWorkflow(childCtx, childWorkflow).Get(ctx, &diff)
		return diff, err
	}

	s.env.RegisterWorkflow(childWorkflow)
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var diff string
	s.NoError(s.env.GetWorkflowResult(&diff))
	s.Equal("diff content", diff)
	s.GreaterOrEqual(callCount, 2, "GitDiffActivity should be invoked at least twice (one failure, one retry success)")
}

// TestGetGitDiffFailsFastWhenHumanInTheLoopDisabled ensures the original
// error is returned without prompting the user when DisableHumanInTheLoop is
// set, preserving existing semantics for unattended runs.
func (s *GitDiffUserRetryTestSuite) TestGetGitDiffFailsFastWhenHumanInTheLoopDisabled() {
	callCount := 0
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			callCount++
			return "", errors.New("simulated diff failure")
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

		dCtx := s.newDevContext(ctx, true)
		return GetGitDiff(dCtx, "main", false)
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "simulated diff failure")
	s.False(requestForUserSeen, "no user-continue prompt should be issued when DisableHumanInTheLoop is set")
	s.Equal(1, callCount, "GitDiffActivity should not be retried when DisableHumanInTheLoop is set")
}

func TestGitDiffUserRetryTestSuite(t *testing.T) {
	suite.Run(t, new(GitDiffUserRetryTestSuite))
}
