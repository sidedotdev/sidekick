package dev

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/utils"
)

func containsAll(text string, substrings ...string) bool {
	for _, substring := range substrings {
		if !strings.Contains(text, substring) {
			return false
		}
	}
	return true
}

// realReviewResolveChildId is the child workflow id the parent test workflow
// signals user responses back to, standing in for the task workflow that
// relays user responses in production.
const realReviewResolveChildId = "review-resolve-child"

// realFlowUserResponder answers the merge approval requests a flow raises,
// rejecting every round but the last so the production review loop iterates.
type realFlowUserResponder struct {
	rejectionMessage string
	approveAfter     int
	seen             int
}

func (r *realFlowUserResponder) respond(req flow_action.RequestForUser) (flow_action.UserResponse, error) {
	if req.RequestKind != flow_action.RequestKindMergeApproval {
		return flow_action.UserResponse{}, fmt.Errorf("unexpected user request kind %q: %s", req.RequestKind, req.Content)
	}
	r.seen++
	approved := r.seen > r.approveAfter
	response := flow_action.UserResponse{
		TargetWorkflowId: req.OriginWorkflowId,
		FlowActionId:     req.FlowActionId,
		Approved:         &approved,
	}
	if approved {
		response.Content = "looks good"
	} else {
		response.Content = r.rejectionMessage
	}
	return response, nil
}

// runFlowWithUserResponses executes the given flow as a child workflow and
// answers its user requests from the parent, which is how user responses reach
// a flow in production.
func (h *reviewDiffsFlowHarness) runFlowWithUserResponses(flow func(ctx workflow.Context) error, responder *realFlowUserResponder) {
	h.t.Helper()

	child := func(ctx workflow.Context) error {
		return flow(utils.NoRetryCtx(ctx))
	}
	h.env.RegisterWorkflow(child)

	parent := func(ctx workflow.Context) error {
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: realReviewResolveChildId,
		})
		childRun := workflow.ExecuteChildWorkflow(childCtx, child)

		requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		unblocked := workflow.GetSignalChannel(ctx, flow_action.SignalNameSubtaskUnblocked)
		var childErr error
		childDone := false
		var pending []flow_action.RequestForUser

		selector := workflow.NewSelector(ctx)
		selector.AddFuture(childRun, func(f workflow.Future) {
			childErr = f.Get(ctx, nil)
			childDone = true
		})
		selector.AddReceive(requests, func(c workflow.ReceiveChannel, _ bool) {
			var req flow_action.RequestForUser
			c.Receive(ctx, &req)
			pending = append(pending, req)
		})
		selector.AddReceive(unblocked, func(c workflow.ReceiveChannel, _ bool) {
			var signal flow_action.SubtaskUnblocked
			c.Receive(ctx, &signal)
		})

		for !childDone {
			selector.Select(ctx)
			for _, req := range pending {
				response, err := responder.respond(req)
				if err != nil {
					return err
				}
				if err := workflow.SignalExternalWorkflow(ctx, realReviewResolveChildId, "",
					flow_action.UserResponseSignalName(req.FlowActionId), response).Get(ctx, nil); err != nil {
					return err
				}
			}
			pending = nil
		}
		return childErr
	}
	h.env.RegisterWorkflow(parent)

	h.env.ExecuteWorkflow(parent)
	require.True(h.t, h.env.IsWorkflowCompleted())
	require.NoError(h.t, h.env.GetWorkflowError())
}

// TestReviewAndResolveRealFlow drives the production review/resolve loop, whose
// coding rounds are mocked at the model level while every git operation is
// real, to verify the diff carried between review rounds: the human reviews the
// full diff plus the work done since their last review, and the auto-reviewer
// judges exactly that same since-review diff.
func TestReviewAndResolveRealFlow(t *testing.T) {
	t.Parallel()

	h := newReviewDiffsFlowHarness(t)
	h.repos.work.stage("pre_feedback.txt", "PRE_FEEDBACK_WORK\n")
	h.repos.work.stage("shared.txt", "line one\n"+reportedReviewVersionCheck+"\nline two\nline three\n")
	h.codingResponse = strings.Join([]string{
		"Here is the fix.",
		"",
		"~~~~",
		"edit_block:1",
		"post_feedback.txt",
		"<<<<<<< CREATE_FILE",
		"=======",
		"POST_FEEDBACK_WORK",
		">>>>>>> NEW_LINES",
		"~~~~",
	}, "\n")
	h.roundHooks = map[int]func(t *testing.T, repos *flowRepos){
		1: func(t *testing.T, repos *flowRepos) {
			repos.work.stage("shared.txt", "line one\nline two\nline three\n")
		},
	}

	const reviewMessage = "please also handle the edge case"
	target := "main"
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		dCtx := h.devContext(ctx)
		dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
		return reviewAndResolve(dCtx, MergeWithReviewParams{
			Requirements:   "original requirements",
			StartBranch:    &target,
			CommitRequired: true,
		})
	}, &realFlowUserResponder{rejectionMessage: reviewMessage, approveAfter: 1})

	require.True(t, h.codingResponseSent, "the coding round must have run and authored its changes")
	require.GreaterOrEqual(t, len(h.mergeApprovals), 2, "the loop must review twice: once rejected, once approved")
	require.NotEmpty(t, h.fulfillmentDiffs)

	firstApproval := h.mergeApprovals[0]
	assert.Contains(t, firstApproval.Diff, "PRE_FEEDBACK_WORK")
	assert.Empty(t, firstApproval.DiffSinceLastReview, "the first review has no prior review to diff against")

	lastApproval := h.mergeApprovals[len(h.mergeApprovals)-1]
	assert.Contains(t, lastApproval.Diff, "PRE_FEEDBACK_WORK")
	assert.Contains(t, lastApproval.Diff, "POST_FEEDBACK_WORK")
	assert.Contains(t, lastApproval.DiffSinceLastReview, "POST_FEEDBACK_WORK",
		"work done after the review is what the human reviews next")
	assert.NotContains(t, lastApproval.DiffSinceLastReview, "PRE_FEEDBACK_WORK",
		"already reviewed work must not be shown as new")

	workDiff := h.fulfillmentDiffs[len(h.fulfillmentDiffs)-1]
	prompt := h.fulfillmentPromptContaining(workDiff)
	assert.Contains(t, prompt, normalizeReviewDiff(firstApproval.Diff),
		"auto-review needs the original user-reviewed work in its complete prompt")
	assert.Contains(t, workDiff, normalizeReviewDiff(lastApproval.DiffSinceLastReview),
		"auto-review also needs the changes since user rejection")
	assert.Contains(t, prompt, "Work Done So Far:")
	assert.Contains(t, prompt, reviewMessage)

	assert.Contains(t, firstApproval.Diff, "+"+reportedReviewVersionCheck)
	assert.NotContains(t, lastApproval.Diff, "shared.txt")
	for name, diff := range map[string]string{
		"merge approval":       lastApproval.DiffSinceLastReview,
		"criteria fulfillment": workDiff,
	} {
		assert.Contains(t, diff, "\n-"+reportedReviewVersionCheck+"\n", name)
		assert.NotContains(t, diff, "Reverted since last review", name)
		assert.NotContains(t, diff, "previously added", name)
		assert.NotContains(t, diff, "previously removed", name)
	}
}

func TestReviewAndResolvePreservesFullDiffAcrossNoOpReviews(t *testing.T) {
	t.Parallel()

	h := newReviewDiffsFlowHarness(t)
	h.repos.work.stage("pre_feedback.txt", "PRE_FEEDBACK_WORK\n")

	target := "main"
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		dCtx := h.devContext(ctx)
		dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
		return reviewAndResolve(dCtx, MergeWithReviewParams{
			Requirements:   "original requirements",
			StartBranch:    &target,
			CommitRequired: true,
		})
	}, &realFlowUserResponder{
		rejectionMessage: "please reconsider the existing work",
		approveAfter:     2,
	})

	require.Len(t, h.mergeApprovals, 3)
	firstDiff := h.mergeApprovals[0].Diff
	require.Contains(t, firstDiff, "PRE_FEEDBACK_WORK")
	for round, approval := range h.mergeApprovals {
		assert.Equal(t, firstDiff, approval.Diff, "review round %d must retain the full diff", round)
		assert.Empty(t, approval.DiffSinceLastReview, "review round %d has no new changes", round)
	}

	require.Len(t, h.fulfillmentDiffs, 2)
	for round, diff := range h.fulfillmentDiffs {
		prompt := h.fulfillmentPromptContaining(diff)
		assert.Contains(t, prompt, "PRE_FEEDBACK_WORK", "reviewer round %d needs the prior work", round)
		assert.Contains(t, prompt, "Here are the changes since the last review", "reviewer round %d must remain incremental", round)
		assert.Contains(t, prompt, "No changes since the last review.", "reviewer round %d has no new changes", round)
	}
}
