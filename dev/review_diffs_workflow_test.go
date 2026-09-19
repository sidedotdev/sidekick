package dev

import (
	"encoding/json"
	"errors"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/temporalmeta"
	"sidekick/utils"
)

// mergeApprovalOutcome exposes everything getMergeApproval returns so tests can
// assert on the diff carried back to callers as well as the response itself.
type mergeApprovalOutcome struct {
	Response MergeApprovalResponse
	GitDiff  string
	TreeHash string
}

// decodeMergeApprovalInfo reads the merge approval params out of flow action
// params, which arrive either as the original struct or as the generic map
// produced by round-tripping through the data converter.
func decodeMergeApprovalInfo(raw any) (MergeApprovalParams, error) {
	if info, ok := raw.(MergeApprovalParams); ok {
		return info, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return MergeApprovalParams{}, err
	}
	var info MergeApprovalParams
	if err := json.Unmarshal(encoded, &info); err != nil {
		return MergeApprovalParams{}, err
	}
	return info, nil
}

// reviewDiffsApprovalWorkflow auto-merges so no human interaction is needed.
// Disabling human-in-the-loop keeps activity failures from turning into retry
// prompts, which auto-merge callers can't answer anyway.
func (s *AutoMergeApprovalTestSuite) reviewDiffsApprovalWorkflow(target, priorReviewDiff string, disableHumanInTheLoop bool) func(ctx workflow.Context) (mergeApprovalOutcome, error) {
	return func(ctx workflow.Context) (mergeApprovalOutcome, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: &flow_action.GlobalState{},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
				DisableHumanInTheLoop: disableHumanInTheLoop,
			},
			Worktree: &domain.Worktree{
				Name: "side/sub-task",
			},
			RepoConfig: common.RepoConfig{},
		}
		response, gitDiff, treeHash, err := getMergeApproval(dCtx, target, true, "", priorReviewDiff, true)
		return mergeApprovalOutcome{Response: response, GitDiff: gitDiff, TreeHash: treeHash}, err
	}
}

// TestMergeApprovalUsesGeneratedReviewDiffs verifies the current version of the
// merge approval flow derives both diffs from GenerateReviewDiffsActivity,
// identifies the last review by its diff rather than a gc-prunable tree hash,
// and never writes a throwaway tree object.
func (s *AutoMergeApprovalTestSuite) TestMergeApprovalUsesGeneratedReviewDiffs() {
	const priorReviewDiff = "prior review diff"

	var reviewDiffParams []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			reviewDiffParams = append(reviewDiffParams, args.Get(1).(coding.GenerateReviewDiffsParams))
		}).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "full diff", SinceDiff: "since diff"}, nil)

	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			s.Fail("no throwaway tree object should be written for the review-diffs mechanism")
		}).
		Return("tree-hash", nil).Maybe()

	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)

	testWorkflow := s.reviewDiffsApprovalWorkflow("main", priorReviewDiff, false)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.Equal("full diff", outcome.GitDiff)
	s.Empty(outcome.TreeHash)

	s.Require().Len(reviewDiffParams, 1)
	s.Equal("main", reviewDiffParams[0].StartPoint)
	s.Equal("main", reviewDiffParams[0].BaseBranch)
	s.Equal(priorReviewDiff, reviewDiffParams[0].PriorReviewDiff)

	s.Require().NotEmpty(persistedActions)
	actionParams := persistedActions[len(persistedActions)-1].ActionParams
	s.Equal(priorReviewDiff, actionParams["lastReviewDiff"])
	s.NotContains(actionParams, "lastReviewTreeHash")
	mergeApprovalInfo, err := decodeMergeApprovalInfo(actionParams["mergeApprovalInfo"])
	s.Require().NoError(err)
	s.Equal("full diff", mergeApprovalInfo.Diff)
	s.Equal("since diff", mergeApprovalInfo.DiffSinceLastReview)
}

// TestMergeApprovalNotesSinceReviewDiffFailure verifies that a failure to
// compare against the last review still yields a reviewable full diff, with the
// failure surfaced in place of the since-review section.
func (s *AutoMergeApprovalTestSuite) TestMergeApprovalNotesSinceReviewDiffFailure() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{
			FullDiff:       "full diff",
			SinceDiff:      "full diff",
			SinceDiffError: "interdiff exploded",
		}, nil)

	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)

	testWorkflow := s.reviewDiffsApprovalWorkflow("main", "prior review diff", true)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.Equal("full diff", outcome.GitDiff)

	s.Require().NotEmpty(persistedActions)
	actionParams := persistedActions[len(persistedActions)-1].ActionParams
	mergeApprovalInfo, err := decodeMergeApprovalInfo(actionParams["mergeApprovalInfo"])
	s.Require().NoError(err)
	s.Equal("full diff", mergeApprovalInfo.Diff)
	s.Contains(mergeApprovalInfo.DiffSinceLastReview, "Failed to generate diff since last review")
}

// TestMergeApprovalLegacyVersionUsesTreeHash verifies workflows started before
// the review-diffs activity existed keep using the tree-hash based mechanism.
func (s *AutoMergeApprovalTestSuite) TestMergeApprovalLegacyVersionUsesTreeHash() {
	s.env.OnGetVersion("review-diffs-v6-gate", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	s.env.OnGetVersion("diff-since-last-review", workflow.DefaultVersion, 5).Return(workflow.Version(5))
	s.setupCommonMocks()

	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)

	testWorkflow := s.reviewDiffsApprovalWorkflow("main", "", false)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.Equal("diff content", outcome.GitDiff)
	s.Equal("tree-hash", outcome.TreeHash)

	s.Require().NotEmpty(persistedActions)
	actionParams := persistedActions[len(persistedActions)-1].ActionParams
	s.Contains(actionParams, "lastReviewTreeHash")
	s.NotContains(actionParams, "lastReviewDiff")
}

// reviewDiffsApprovalWorkflow runs a merge approval interaction. A pending
// "go next" user action makes failed diff regeneration surface immediately
// instead of looping on retry prompts.
func (s *MergeStrategyRoundTripTestSuite) reviewDiffsApprovalWorkflow(priorReviewDiff string, pendingGoNext bool) func(ctx workflow.Context) (MergeApprovalResponse, error) {
	return func(ctx workflow.Context) (MergeApprovalResponse, error) {
		ctx = utils.NoRetryCtx(ctx)
		globalState := &flow_action.GlobalState{}
		if pendingGoNext {
			globalState.SetUserAction(flow_action.UserActionGoNext)
		}
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: globalState,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			RepoConfig: common.RepoConfig{},
		}

		mergeParams := MergeApprovalParams{
			SourceBranch:         "side/test-branch",
			DefaultTargetBranch:  "main",
			Diff:                 "original full diff",
			DiffSinceLastReview:  "original since diff",
			DefaultMergeStrategy: MergeStrategySquash,
		}

		return GetUserMergeApproval(dCtx, "Please review", map[string]any{
			"mergeApprovalInfo": mergeParams,
			"lastReviewDiff":    priorReviewDiff,
		})
	}
}

// humanMergeApprovalWorkflow requests merge approval from a human, so the
// request params it builds are the ones the merge approval UI receives.
func (s *MergeStrategyRoundTripTestSuite) humanMergeApprovalWorkflow(priorReviewDiff string) func(ctx workflow.Context) (mergeApprovalOutcome, error) {
	return func(ctx workflow.Context) (mergeApprovalOutcome, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: &flow_action.GlobalState{},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree: &domain.Worktree{
				Name: "side/sub-task",
			},
			RepoConfig: common.RepoConfig{},
		}
		response, gitDiff, treeHash, err := getMergeApproval(dCtx, "main", true, "", priorReviewDiff, false)
		return mergeApprovalOutcome{Response: response, GitDiff: gitDiff, TreeHash: treeHash}, err
	}
}

// approvalParent approves the merge approval child workflow's request without
// changing any params.
func (s *MergeStrategyRoundTripTestSuite) approvalParent(testWorkflow interface{}) func(ctx workflow.Context) (mergeApprovalOutcome, error) {
	return func(ctx workflow.Context) (mergeApprovalOutcome, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)

		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)

			approved := true
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				FlowActionId:     req.FlowActionId,
				Approved:         &approved,
			}).Get(ctx, nil)
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-merge-approval-workflow",
		})
		var result mergeApprovalOutcome
		err := workflow.ExecuteChildWorkflow(childCtx, testWorkflow).Get(ctx, &result)
		return result, err
	}
}

// TestHumanMergeApprovalCarriesLastReviewDiff verifies the human merge approval
// request identifies the last review by its diff rather than a gc-prunable tree
// hash, matching what the auto-merge path records.
func (s *MergeStrategyRoundTripTestSuite) TestHumanMergeApprovalCarriesLastReviewDiff() {
	const priorReviewDiff = "prior review diff"
	persistedActions := s.setupReviewDiffsMocks()

	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "full diff", SinceDiff: "since diff"}, nil)

	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			s.Fail("no throwaway tree object should be written for the review-diffs mechanism")
		}).
		Return("tree-hash", nil).Maybe()

	testWorkflow := s.humanMergeApprovalWorkflow(priorReviewDiff)
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := s.approvalParent(testWorkflow)
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.True(outcome.Response.Approved)
	s.Equal("full diff", outcome.GitDiff)
	s.Empty(outcome.TreeHash)

	s.Require().NotEmpty(*persistedActions)
	actionParams := (*persistedActions)[len(*persistedActions)-1].ActionParams
	s.Equal(priorReviewDiff, actionParams["lastReviewDiff"])
	s.NotContains(actionParams, "lastReviewTreeHash")
	mergeApprovalInfo, err := decodeMergeApprovalInfo(actionParams["mergeApprovalInfo"])
	s.Require().NoError(err)
	s.Equal("full diff", mergeApprovalInfo.Diff)
	s.Equal("since diff", mergeApprovalInfo.DiffSinceLastReview)
}

// targetBranchUpdateParent drives a merge approval child workflow through a
// param-only update followed by approval, mirroring what the UI sends when the
// user switches target branch and toggles whitespace handling.
func targetBranchUpdateParent[T any](testWorkflow interface{}, newTarget string) func(ctx workflow.Context) (T, error) {
	return func(ctx workflow.Context) (T, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)

		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)

			signalName := flow_action.UserResponseSignalName(req.FlowActionId)

			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", signalName, flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				FlowActionId:     req.FlowActionId,
				Params: map[string]interface{}{
					"targetBranch":     newTarget,
					"ignoreWhitespace": true,
				},
			}).Get(ctx, nil)

			approved := true
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", signalName, flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				FlowActionId:     req.FlowActionId,
				Approved:         &approved,
			}).Get(ctx, nil)
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-merge-approval-workflow",
		})
		var result T
		err := workflow.ExecuteChildWorkflow(childCtx, testWorkflow).Get(ctx, &result)
		return result, err
	}
}

func (s *MergeStrategyRoundTripTestSuite) setupReviewDiffsMocks() *[]domain.FlowAction {
	persistedActions := &[]domain.FlowAction{}
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			*persistedActions = append(*persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	return persistedActions
}

// TestTargetBranchChangeRegeneratesReviewDiffs verifies that changing the
// target branch mid-review regenerates both diffs against the new target while
// still comparing against the prior review's diff.
func (s *MergeStrategyRoundTripTestSuite) TestTargetBranchChangeRegeneratesReviewDiffs() {
	const priorReviewDiff = "prior review diff"
	persistedActions := s.setupReviewDiffsMocks()

	var reviewDiffParams []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			reviewDiffParams = append(reviewDiffParams, args.Get(1).(coding.GenerateReviewDiffsParams))
		}).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "refreshed full diff", SinceDiff: "refreshed since diff"}, nil)

	testWorkflow := s.reviewDiffsApprovalWorkflow(priorReviewDiff, false)
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := targetBranchUpdateParent[MergeApprovalResponse](testWorkflow, "other-target")
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("other-target", result.TargetBranch)

	s.Require().Len(reviewDiffParams, 1)
	s.Equal("other-target", reviewDiffParams[0].StartPoint)
	s.Equal("other-target", reviewDiffParams[0].BaseBranch)
	s.Equal(priorReviewDiff, reviewDiffParams[0].PriorReviewDiff)
	s.True(reviewDiffParams[0].IgnoreWhitespace)

	s.Require().NotEmpty(*persistedActions)
	actionParams := (*persistedActions)[len(*persistedActions)-1].ActionParams
	mergeApprovalInfo, err := decodeMergeApprovalInfo(actionParams["mergeApprovalInfo"])
	s.Require().NoError(err)
	s.Equal("refreshed full diff", mergeApprovalInfo.Diff)
	s.Equal("refreshed since diff", mergeApprovalInfo.DiffSinceLastReview)
	s.Equal("other-target", mergeApprovalInfo.DefaultTargetBranch)
}

// TestTargetBranchChangeReturnsReviewedDiff verifies the diff handed back to
// callers is the one the user actually reviewed after changing params mid-review,
// since that diff is what the next review round compares against.
func (s *MergeStrategyRoundTripTestSuite) TestTargetBranchChangeReturnsReviewedDiff() {
	s.setupReviewDiffsMocks()

	beforeParamChange := mock.MatchedBy(func(params coding.GenerateReviewDiffsParams) bool {
		return !params.IgnoreWhitespace
	})
	afterParamChange := mock.MatchedBy(func(params coding.GenerateReviewDiffsParams) bool {
		return params.IgnoreWhitespace
	})

	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, beforeParamChange).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "initial full diff", SinceDiff: "initial since diff"}, nil)
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, afterParamChange).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "regenerated full diff", SinceDiff: "regenerated since diff"}, nil)

	testWorkflow := s.humanMergeApprovalWorkflow("prior review diff")
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := targetBranchUpdateParent[mergeApprovalOutcome](testWorkflow, "other-target")
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.True(outcome.Response.Approved)
	s.Equal("other-target", outcome.Response.TargetBranch)
	s.Equal("regenerated full diff", outcome.GitDiff)
}

// TestTargetBranchChangeToEmptyDiff verifies an empty regenerated diff is
// carried back as the reviewed diff, since a target that already contains all
// our changes legitimately has nothing left to review.
func (s *MergeStrategyRoundTripTestSuite) TestTargetBranchChangeToEmptyDiff() {
	s.setupReviewDiffsMocks()

	beforeParamChange := mock.MatchedBy(func(params coding.GenerateReviewDiffsParams) bool {
		return !params.IgnoreWhitespace
	})
	afterParamChange := mock.MatchedBy(func(params coding.GenerateReviewDiffsParams) bool {
		return params.IgnoreWhitespace
	})

	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, beforeParamChange).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "initial full diff", SinceDiff: "initial since diff"}, nil)
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, afterParamChange).
		Return(coding.GenerateReviewDiffsResult{}, nil)

	testWorkflow := s.humanMergeApprovalWorkflow("prior review diff")
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := targetBranchUpdateParent[mergeApprovalOutcome](testWorkflow, "other-target")
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var outcome mergeApprovalOutcome
	s.NoError(s.env.GetWorkflowResult(&outcome))
	s.True(outcome.Response.Approved)
	s.Equal("other-target", outcome.Response.TargetBranch)
	s.Empty(outcome.GitDiff)
}

// TestTargetBranchChangeDropsSinceDiffOnFailure verifies that when comparison
// against the last review degrades after a target change, the review keeps going
// with the new target's full diff and no since-review diff.
func (s *MergeStrategyRoundTripTestSuite) TestTargetBranchChangeDropsSinceDiffOnFailure() {
	persistedActions := s.setupReviewDiffsMocks()

	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{
			FullDiff:       "regenerated full diff",
			SinceDiff:      "regenerated full diff",
			SinceDiffError: "interdiff exploded",
		}, nil)

	testWorkflow := s.reviewDiffsApprovalWorkflow("prior review diff", false)
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := targetBranchUpdateParent[MergeApprovalResponse](testWorkflow, "other-target")
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("other-target", result.TargetBranch)
	s.Equal("regenerated full diff", result.Diff)
	s.Empty(result.DiffSinceLastReview)

	s.Require().NotEmpty(*persistedActions)
	actionParams := (*persistedActions)[len(*persistedActions)-1].ActionParams
	mergeApprovalInfo, err := decodeMergeApprovalInfo(actionParams["mergeApprovalInfo"])
	s.Require().NoError(err)
	s.Equal("other-target", mergeApprovalInfo.DefaultTargetBranch)
	s.Equal("regenerated full diff", mergeApprovalInfo.Diff)
	s.Empty(mergeApprovalInfo.DiffSinceLastReview)
}

// TestTargetBranchChangeFailsOnGitFailure verifies that when git cannot produce
// a diff for the new target, the review stops instead of presenting the previous
// target's diff as the new target's.
func (s *MergeStrategyRoundTripTestSuite) TestTargetBranchChangeFailsOnGitFailure() {
	persistedActions := s.setupReviewDiffsMocks()

	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{}, errors.New("git exploded"))

	testWorkflow := s.reviewDiffsApprovalWorkflow("prior review diff", true) // pending go-next avoids retry prompting
	s.env.RegisterWorkflow(testWorkflow)
	parentWorkflow := targetBranchUpdateParent[MergeApprovalResponse](testWorkflow, "other-target")
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())

	for _, action := range *persistedActions {
		mergeApprovalInfo, err := decodeMergeApprovalInfo(action.ActionParams["mergeApprovalInfo"])
		s.Require().NoError(err)
		if mergeApprovalInfo.DefaultTargetBranch == "other-target" {
			s.NotEqual("original full diff", mergeApprovalInfo.Diff, "the new target must never be shown with the previous target's diff")
		}
	}
}
