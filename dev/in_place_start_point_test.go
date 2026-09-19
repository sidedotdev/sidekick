package dev

import (
	"context"
	"os"
	"path/filepath"
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

// InPlaceStartPointTestSuite covers how work done in the user's own checkout,
// where no worktree isolates it, is anchored for review: everything done after
// the flow started is its work, including commits, while whatever was already
// modified there is not.
type InPlaceStartPointTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer
}

// pinnedReviewOutcome reports what the pin resolved to and what a review round
// based on that pin would show.
type pinnedReviewOutcome struct {
	StartPoint string
	PriorDiff  string
	Diffs      coding.GenerateReviewDiffsResult
}

// pinnedRound is what one auto-review round of a pinned flow compares.
type pinnedRound struct {
	StartPoint     string
	BaseBranch     string
	LastReviewDiff string
}

func (s *InPlaceStartPointTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())

	s.dir = s.T().TempDir()
	runCmd(s.T(), s.dir, "git", "init", "-b", "main")
	runCmd(s.T(), s.dir, "git", "config", "user.email", "test@test.com")
	runCmd(s.T(), s.dir, "git", "config", "user.name", "Test")
	writeAndCommit(s.T(), s.dir, "existing.txt", "line one\n", "initial commit")
	writeAndCommit(s.T(), s.dir, "other.txt", "other line one\n", "second commit")

	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: s.dir})
	s.Require().NoError(err)
	s.envContainer = env.EnvContainer{Env: devEnv}

	s.env.RegisterActivity(git.GitRevParseActivity)
	s.env.RegisterActivity(git.GitDiffActivity)
	var ca *coding.CodingActivities
	s.env.RegisterActivity(ca.GenerateReviewDiffsActivity)

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *InPlaceStartPointTestSuite) newDevContext(ctx workflow.Context, worktree *domain.Worktree) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	gs.SetValue(common.KeyCurrentTargetBranch, "main")
	return DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId:  "test-workspace",
			Context:      ctx,
			FlowScope:    &flow_action.FlowScope{SubflowName: "test-subflow"},
			GlobalState:  gs,
			EnvContainer: &s.envContainer,
		},
		Worktree:   worktree,
		RepoConfig: common.RepoConfig{},
	}
}

func (s *InPlaceStartPointTestSuite) writeFile(name, content string) {
	s.T().Helper()
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0644))
}

// runPinnedReview pins the start point, lets work happen in the repository the
// way a coding round would, then generates the review diffs that work is judged
// by.
func (s *InPlaceStartPointTestSuite) runPinnedReview(worktree *domain.Worktree, work func()) pinnedReviewOutcome {
	s.T().Helper()

	doWork := func(ctx context.Context) error {
		work()
		return nil
	}
	s.env.RegisterActivity(doWork)

	testWorkflow := func(ctx workflow.Context) (pinnedReviewOutcome, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := s.newDevContext(ctx, worktree)

		if err := pinInPlaceReviewStart(dCtx); err != nil {
			return pinnedReviewOutcome{}, err
		}
		pinned := applyPinnedReviewStart(dCtx, CheckWorkInfo{BaseBranch: "main"})
		outcome := pinnedReviewOutcome{StartPoint: pinned.StartPoint, PriorDiff: pinned.LastReviewDiff}
		if pinned.StartPoint == "" {
			return outcome, nil
		}

		if err := workflow.ExecuteActivity(ctx, doWork).Get(ctx, nil); err != nil {
			return outcome, err
		}

		var err error
		outcome.Diffs, err = generateReviewDiffs(dCtx, pinned.StartPoint, pinned.BaseBranch, pinned.LastReviewDiff, true)
		return outcome, err
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())

	var outcome pinnedReviewOutcome
	s.Require().NoError(s.env.GetWorkflowResult(&outcome))
	return outcome
}

// TestReviewsWorkDoneAfterThePinIncludingCommits is the case in-place flows
// previously got wrong: a commit made during the flow was invisible to review,
// which only saw staged changes.
func (s *InPlaceStartPointTestSuite) TestReviewsWorkDoneAfterThePinIncludingCommits() {
	s.writeFile("existing.txt", "line one\npreexisting staged\n")
	runCmd(s.T(), s.dir, "git", "add", "existing.txt")
	s.writeFile("other.txt", "other line one\npreexisting unstaged\n")

	outcome := s.runPinnedReview(nil, func() {
		s.writeFile("feature.txt", "agent commit line\n")
		runCmd(s.T(), s.dir, "git", "add", "-A")
		runCmd(s.T(), s.dir, "git", "commit", "-m", "agent commit")

		s.writeFile("existing.txt", "line one\npreexisting staged\nagent staged line\n")
		runCmd(s.T(), s.dir, "git", "add", "existing.txt")
	})

	s.Require().NotEmpty(outcome.StartPoint)
	s.Contains(outcome.PriorDiff, "preexisting staged")
	s.Contains(outcome.PriorDiff, "preexisting unstaged")

	s.Contains(outcome.Diffs.FullDiff, "+agent commit line", "committed work is part of the work under review")
	s.Contains(outcome.Diffs.FullDiff, "+agent staged line")
	s.Contains(outcome.Diffs.FullDiff, "+preexisting staged",
		"the flow's commit swept in what was already staged, so only the prior review diff can tell it apart")

	s.Empty(outcome.Diffs.SinceDiffError)
	s.Contains(outcome.Diffs.SinceDiff, "+agent commit line")
	s.Contains(outcome.Diffs.SinceDiff, "+agent staged line")
	s.NotContains(outcome.Diffs.SinceDiff, "+preexisting staged")
	s.NotContains(outcome.Diffs.SinceDiff, "+preexisting unstaged")
}

// TestEveryRoundSharesOnePin covers flows that code again after review
// feedback: re-pinning mid-flow would measure later rounds from a moved start
// point and lose the earlier work.
func (s *InPlaceStartPointTestSuite) TestEveryRoundSharesOnePin() {
	revParseCalls := 0
	s.env.OnActivity(git.GitRevParseActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params git.GitRevParseParams) (git.GitRevParseResult, error) {
			revParseCalls++
			return git.GitRevParseResult{CommitHash: "in-place-start-sha"}, nil
		},
	)

	testWorkflow := func(ctx workflow.Context) ([]pinnedRound, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := s.newDevContext(ctx, nil)

		if err := pinInPlaceReviewStart(dCtx); err != nil {
			return nil, err
		}

		firstRound := applyPinnedReviewStart(dCtx, CheckWorkInfo{BaseBranch: "main"})
		secondRound := applyPinnedReviewStart(dCtx, CheckWorkInfo{BaseBranch: "main", LastReviewDiff: "diff at first review"})
		return []pinnedRound{
			{StartPoint: firstRound.StartPoint, BaseBranch: firstRound.BaseBranch, LastReviewDiff: firstRound.LastReviewDiff},
			{StartPoint: secondRound.StartPoint, BaseBranch: secondRound.BaseBranch, LastReviewDiff: secondRound.LastReviewDiff},
		}, nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())

	var rounds []pinnedRound
	s.Require().NoError(s.env.GetWorkflowResult(&rounds))

	s.Equal(1, revParseCalls, "the start point is pinned once per flow")
	s.Require().Len(rounds, 2)
	for i, round := range rounds {
		s.Equal("in-place-start-sha", round.StartPoint, "round %d", i)
		s.Empty(round.BaseBranch, "round %d", i)
	}
	s.Equal("diff at first review", rounds[1].LastReviewDiff,
		"once there is a review to compare against, it replaces the pre-existing changes")
}

// TestWorktreeFlowsAreNotPinned keeps worktree flows comparing against their
// base branch, where the branch itself already isolates the work.
func (s *InPlaceStartPointTestSuite) TestWorktreeFlowsAreNotPinned() {
	revParseCalls := 0
	s.env.OnActivity(git.GitRevParseActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params git.GitRevParseParams) (git.GitRevParseResult, error) {
			revParseCalls++
			return git.GitRevParseResult{CommitHash: "unexpected"}, nil
		},
	).Maybe()

	outcome := s.runPinnedReview(&domain.Worktree{Name: "side/task"}, func() {})

	s.Empty(outcome.StartPoint)
	s.Zero(revParseCalls)
}

func (s *InPlaceStartPointTestSuite) TestLegacyVersionDoesNotPin() {
	s.env.OnGetVersion("in-place-start-point", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	revParseCalls := 0
	s.env.OnActivity(git.GitRevParseActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params git.GitRevParseParams) (git.GitRevParseResult, error) {
			revParseCalls++
			return git.GitRevParseResult{CommitHash: "unexpected"}, nil
		},
	).Maybe()

	outcome := s.runPinnedReview(nil, func() {})

	s.Empty(outcome.StartPoint)
	s.Zero(revParseCalls)
}

func TestInPlaceStartPointTestSuite(t *testing.T) {
	suite.Run(t, new(InPlaceStartPointTestSuite))
}
