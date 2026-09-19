package dev

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

// StepStartPinTestSuite covers how a plan step's review diffs are anchored: the
// step's start commit is pinned once, and each round after the first reviews only
// what changed since the diff of the previous round.
type StepStartPinTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

type stepReviewRound struct {
	StartPoint     string
	BaseBranch     string
	LastReviewDiff string
}

func (s *StepStartPinTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *StepStartPinTestSuite) newDevContext(ctx workflow.Context) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	gs.SetValue(common.KeyCurrentTargetBranch, "main")
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

// runTwoRounds simulates two auto-review rounds within one plan step, reporting
// what each round would have asked the review-diff generation for.
func (s *StepStartPinTestSuite) runTwoRounds() []stepReviewRound {
	testWorkflow := func(ctx workflow.Context) ([]stepReviewRound, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := s.newDevContext(ctx)

		reviewState, err := pinStepReviewState(dCtx)
		if err != nil {
			return nil, err
		}

		rounds := []stepReviewRound{newStepReviewRound(reviewState.checkWorkInfo(dCtx, CheckWorkInfo{}))}
		rounds = append(rounds, newStepReviewRound(reviewState.checkWorkInfo(dCtx, CheckWorkInfo{})))
		return rounds, nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())

	var rounds []stepReviewRound
	s.Require().NoError(s.env.GetWorkflowResult(&rounds))
	return rounds
}

func newStepReviewRound(info CheckWorkInfo) stepReviewRound {
	return stepReviewRound{
		StartPoint:     info.StartPoint,
		BaseBranch:     info.BaseBranch,
		LastReviewDiff: info.LastReviewDiff,
	}
}

func (s *StepStartPinTestSuite) TestPinsStepStartWithoutAutoReviewBaseline() {
	var revParseParams []git.GitRevParseParams
	s.env.OnActivity(git.GitRevParseActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params git.GitRevParseParams) (git.GitRevParseResult, error) {
			revParseParams = append(revParseParams, params)
			return git.GitRevParseResult{CommitHash: "step-start-sha"}, nil
		},
	)

	rounds := s.runTwoRounds()

	s.Require().Len(revParseParams, 1, "the step start is pinned once per step")
	s.Equal("HEAD", revParseParams[0].Ref)

	s.Require().Len(rounds, 2)
	s.Equal("step-start-sha", rounds[0].StartPoint)
	s.Equal("main", rounds[0].BaseBranch)
	s.Empty(rounds[0].LastReviewDiff, "the first round has no prior review to compare against")

	s.Equal("step-start-sha", rounds[1].StartPoint)
	s.Equal("main", rounds[1].BaseBranch)
	s.Empty(rounds[1].LastReviewDiff, "automatic reviews must include unchanged work")
}

func (s *StepStartPinTestSuite) TestLegacyVersionDoesNotPinStepStart() {
	s.env.OnGetVersion("step-start-pin", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	revParseCalls := 0
	s.env.OnActivity(git.GitRevParseActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params git.GitRevParseParams) (git.GitRevParseResult, error) {
			revParseCalls++
			return git.GitRevParseResult{CommitHash: "step-start-sha"}, nil
		},
	).Maybe()

	rounds := s.runTwoRounds()

	s.Zero(revParseCalls)
	s.Require().Len(rounds, 2)
	for i, round := range rounds {
		s.Empty(round.StartPoint, "round %d", i)
		s.Empty(round.BaseBranch, "round %d", i)
		s.Empty(round.LastReviewDiff, "round %d", i)
	}
}

func TestStepStartPinTestSuite(t *testing.T) {
	suite.Run(t, new(StepStartPinTestSuite))
}
