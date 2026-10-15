package dev

import (
	"context"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

func (s *MergeStrategyRoundTripTestSuite) TestIdenticalReviewTextRetainsBaselineIdentity() {
	s.checkReviewBaselineIdentity("identical displayed patch")
}

func (s *MergeStrategyRoundTripTestSuite) TestEmptyReviewRetainsBaselineIdentity() {
	s.checkReviewBaselineIdentity("")
}

func (s *MergeStrategyRoundTripTestSuite) checkReviewBaselineIdentity(display string) {
	s.setupReviewDiffsMocks()
	snapshots := []*coding.ReviewDiffSnapshot{
		{BaseCommit: "original", StartPoint: "main", TotalDiff: "original total"},
		{BaseCommit: "before-switch", StartPoint: "main", TotalDiff: "before-switch total"},
		{BaseCommit: "reviewed", StartPoint: "other-target", TotalDiff: "reviewed total"},
		{BaseCommit: "criteria-only", StartPoint: "other-target", TotalDiff: "criteria-only total"},
		{BaseCommit: "second-review", StartPoint: "other-target", TotalDiff: "second-review total"},
	}
	calls := 0
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, params coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			expectedPrior := snapshots[0]
			if calls >= 2 {
				expectedPrior = snapshots[2]
			}
			s.Equal(display, params.PriorReviewDiff)
			s.Equal(expectedPrior, params.PriorReviewSnapshot, "baseline on activity call %d", calls)
			s.Equal(calls == 1 || calls == 2, params.IgnoreWhitespace)
			snapshot := snapshots[calls+1]
			calls++
			return coding.GenerateReviewDiffsResult{
				FullDiff: display, SinceDiff: "incremental", ReviewSnapshot: snapshot,
			}, nil
		}).Times(4)

	child := func(ctx workflow.Context) (MergeApprovalResponse, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context: utils.NoRetryCtx(ctx), WorkspaceId: "test-workspace",
				FlowScope:   &flow_action.FlowScope{SubflowName: "test-subflow"},
				GlobalState: &flow_action.GlobalState{},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/unused-test-repo"},
				},
			},
			Worktree: &domain.Worktree{Name: "side/test-branch"},
		}
		prior := coding.ReviewDiffBaseline{Diff: display, Snapshot: snapshots[0]}
		first, diff, _, err := getMergeApproval(dCtx, "main", true, "", prior, false)
		if err != nil {
			return first, err
		}
		s.False(first.Approved)
		s.Equal(display, diff)
		s.Equal(snapshots[2], first.ReviewSnapshot)
		prior = coding.ReviewDiffBaseline{Diff: diff, Snapshot: first.ReviewSnapshot}

		review, _, incremental, err := criteriaFulfillmentReviewDiff(dCtx, CheckWorkInfo{
			BaseBranch: first.TargetBranch, LastReviewDiff: prior.Diff, LastReviewSnapshot: prior.Snapshot,
		}, true)
		if err != nil {
			return first, err
		}
		s.True(incremental)
		s.Equal("incremental", review)
		second, _, _, err := getMergeApproval(dCtx, first.TargetBranch, true, "", prior, false)
		return second, err
	}
	s.env.RegisterWorkflow(child)

	parent := func(ctx workflow.Context) (MergeApprovalResponse, error) {
		requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for round := 0; round < 2; round++ {
				var req flow_action.RequestForUser
				requests.Receive(ctx, &req)
				signal := flow_action.UserResponseSignalName(req.FlowActionId)
				if round == 0 {
					err := workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", signal, flow_action.UserResponse{
						TargetWorkflowId: req.OriginWorkflowId, FlowActionId: req.FlowActionId,
						Params: map[string]interface{}{"targetBranch": "other-target", "ignoreWhitespace": true},
					}).Get(ctx, nil)
					s.NoError(err)
				}
				approved := round == 1
				err := workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", signal, flow_action.UserResponse{
					TargetWorkflowId: req.OriginWorkflowId, FlowActionId: req.FlowActionId,
					Approved: &approved,
				}).Get(ctx, nil)
				s.NoError(err)
			}
		})
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-review-baseline",
		})
		var result MergeApprovalResponse
		err := workflow.ExecuteChildWorkflow(childCtx, child).Get(ctx, &result)
		return result, err
	}
	s.env.RegisterWorkflow(parent)
	s.env.ExecuteWorkflow(parent)
	s.Require().NoError(s.env.GetWorkflowError())
	var result MergeApprovalResponse
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal(snapshots[4], result.ReviewSnapshot)
	s.Equal(4, calls)
}
