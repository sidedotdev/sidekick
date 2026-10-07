package dev

import (
	"fmt"
	"strings"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

const (
	iddMergeApprovalActionType = "user_request.approve.merge"
	iddMergeApprovalPrompt     = "Please review these changes"

	// iddMergeErrorParam names the flow action param carrying the most recent
	// finish failure, which the canvas surfaces in its finish panel.
	iddMergeErrorParam = "mergeError"
)

// iddMergeApproval owns the IDD flow's single merge-approval request. Where
// BasicDev asks for approval once its work is done, the IDD flow raises the
// request up front and keeps it pending for the flow's whole life: the user
// decides when intent is realized well enough to merge. The request's diff is
// refreshed as work lands on the idd worktree branch, and approving it merges
// that branch into the chosen target and finishes the flow.
type iddMergeApproval struct {
	input IddWorkflowInput
	state *IddState

	req    flow_action.RequestForUser
	params MergeApprovalParams

	// targetBranch is the merge target currently selected by the user, which
	// drives diff generation and the eventual merge. The request's
	// DefaultTargetBranch stays the flow's start branch so the canvas can
	// always offer it as the default.
	targetBranch     string
	ignoreWhitespace bool

	// mergeError keeps the most recent finish failure visible on the pending
	// request while the user resolves it and approves again.
	mergeError string

	// mergedDiff is the diff captured immediately before merging, kept so the
	// completed request stays viewable once the worktree is gone.
	mergedDiff string

	// mergeBase pins the target branch's tip as it was before this flow first
	// merged into it. Diffs are generated against that commit from then on, so
	// they keep showing everything the finish merged even after the target has
	// advanced, across as many retries as a failing finish takes.
	mergeBase string

	// refreshCh coalesces requests to regenerate the pending request's diff.
	refreshCh workflow.Channel

	// finishedCh reports a successful finish to the main workflow loop, which
	// owns the workflow's exit.
	finishedCh workflow.Channel
}

// startIddMergeApproval raises the pending merge-approval request and starts
// the coroutine that services it until an approval finishes the flow.
func startIddMergeApproval(dCtx DevContext, input IddWorkflowInput, state *IddState) *iddMergeApproval {
	ma := &iddMergeApproval{
		input:        input,
		state:        state,
		targetBranch: state.DefaultTargetBranch,
		refreshCh:    workflow.NewBufferedChannel(dCtx, 1),
		finishedCh:   workflow.NewBufferedChannel(dCtx, 1),
	}
	state.mergeApproval = ma
	workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
		ma.run(dCtx.WithContext(goCtx))
	})
	return ma
}

// requestDiffRefresh asks the servicing coroutine to regenerate the pending
// request's diff, e.g. once new work has landed on the idd worktree branch.
func (ma *iddMergeApproval) requestDiffRefresh() {
	if ma.refreshCh == nil {
		return
	}
	// Non-blocking: a queued refresh will observe the same or newer work.
	_ = ma.refreshCh.SendAsync(struct{}{})
}

func (ma *iddMergeApproval) run(dCtx DevContext) {
	log := workflow.GetLogger(dCtx)
	actionCtx := dCtx.NewActionContext(iddMergeApprovalActionType)

	var sourceBranch string
	if dCtx.Worktree != nil {
		sourceBranch = dCtx.Worktree.Name
	}
	ma.params = MergeApprovalParams{
		SourceBranch:         sourceBranch,
		DefaultTargetBranch:  ma.targetBranch,
		DefaultMergeStrategy: MergeStrategyMerge,
	}
	ma.req = flow_action.RequestForUser{
		OriginWorkflowId: workflow.GetInfo(dCtx).WorkflowExecution.ID,
		Content:          iddMergeApprovalPrompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		SubflowId:        actionCtx.FlowScope.GetSubflowId(),
		RequestParams:    map[string]any{"mergeApprovalInfo": ma.params},
		RequestKind:      flow_action.RequestKindMergeApproval,
		// The request awaits the user for the flow's whole life while work
		// continues, so its existence must neither pause the flow nor decide
		// the task's status: the parent is notified separately, only once the
		// flow is actually ready for review.
		SkipPauseFlow:    true,
		SkipParentSignal: true,
	}
	ma.refreshDiff(dCtx)
	actionCtx.ActionParams = ma.actionParams()
	// Dev Run reads the current target branch from global state.
	dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, ma.targetBranch)

	response, err := TrackHuman(actionCtx, ma.awaitFinish)
	if err != nil {
		log.Error("IDD merge approval ended without finishing", "Error", err)
		return
	}
	if response.Approved {
		// Sent only after TrackHuman has recorded the completed request, so
		// the merged diff is durable before the workflow exits.
		_ = ma.finishedCh.SendAsync(struct{}{})
	}
}

// awaitFinish services the pending request until an approval leads to a
// successful finish. Diff refreshes, option changes and rejections all update
// the request in place and keep waiting, so it stays available to the user.
func (ma *iddMergeApproval) awaitFinish(actionCtx DevActionContext, flowAction *domain.FlowAction) (MergeApprovalResponse, error) {
	dCtx := actionCtx.DevContext
	log := workflow.GetLogger(dCtx)
	ma.req.FlowActionId = flowAction.Id
	responseCh := workflow.GetSignalChannel(dCtx, flow_action.UserResponseSignalName(flowAction.Id))

	for {
		var response *flow_action.UserResponse
		refreshRequested := false

		selector := workflow.NewNamedSelector(dCtx, "iddMergeApprovalSelector")
		selector.AddReceive(responseCh, func(c workflow.ReceiveChannel, _ bool) {
			var received flow_action.UserResponse
			c.Receive(dCtx, &received)
			response = &received
		})
		selector.AddReceive(ma.refreshCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(dCtx, nil)
			refreshRequested = true
		})
		selector.Select(dCtx)

		if dCtx.Err() != nil {
			return MergeApprovalResponse{}, dCtx.Err()
		}

		if refreshRequested {
			ma.refreshDiff(dCtx)
			ma.persistPending(dCtx, flowAction)
			continue
		}

		if ma.applyResponseOptions(dCtx, *response) {
			ma.refreshDiff(dCtx)
			ma.persistPending(dCtx, flowAction)
		}

		// A response without a decision only carries updated options.
		if response.Approved == nil {
			continue
		}

		// Responses carrying a decision are recorded as completing the request
		// before the workflow sees them, so every outcome other than a
		// successful finish has to re-open it explicitly.
		if !*response.Approved {
			// A rejection just means "not yet": the user redirects an IDD flow
			// by editing intent, not through this request, so the feedback is
			// only logged and the request stays available for a later approval.
			log.Info("IDD merge approval rejected", "Feedback", response.Content)
			ma.mergeError = ""
			ma.persistPending(dCtx, flowAction)
			continue
		}

		if err := ma.finish(dCtx); err != nil {
			log.Error("Failed to finish idd flow", "Error", err)
			ma.mergeError = err.Error()
			ma.persistPending(dCtx, flowAction)
			continue
		}

		// The worktree is gone by now, so the diff captured during the finish
		// is the only remaining record of what was merged.
		ma.mergeError = ""
		ma.params.Diff = ma.mergedDiff
		flowAction.ActionParams = ma.actionParams()
		return MergeApprovalResponse{
			Approved:      true,
			TargetBranch:  ma.targetBranch,
			MergeStrategy: MergeStrategyMerge,
			Message:       response.Content,
		}, nil
	}
}

// finish commits any pending intent in the worktree, merges the idd worktree
// branch into the selected target branch, stops the sub-tasks still in flight
// and cleans the worktree up. Returning cleanly lets the workflow's main loop
// end the flow, which completes the parent task.
//
// The flow stops taking new sub-task work for the duration, since work
// dispatched now would land on a branch that is about to be archived. Existing
// sub-tasks are stopped only after a successful merge, so a failure leaves them
// running and the flow open for another attempt.
//
// Merging happens twice when sub-tasks were in flight: their work reaches the
// idd branch through their own auto-merge, which can land while they wind down,
// i.e. after the first merge. The second merge, once they have all settled,
// carries that work across before the branch is archived.
//
// A finish that failed past the merge is retried in full rather than resumed:
// the worktree outlived the failure, so intent edits and sub-task merges can
// have landed since, and merging again is what carries them across. Merging an
// already-merged branch is a no-op, while the diff — taken against the pinned
// pre-merge base — still covers every round.
//
// Every failure is reported to the caller, which re-opens the request with the
// error, so no step is silently skipped on the way to completing the flow.
func (ma *iddMergeApproval) finish(dCtx DevContext) (err error) {
	target := strings.TrimSpace(ma.targetBranch)
	if target == "" {
		return fmt.Errorf("finish idd: no target branch specified")
	}
	if dCtx.Worktree == nil {
		return fmt.Errorf("finish idd: no worktree associated with idd workflow")
	}
	if target == dCtx.Worktree.Name {
		return fmt.Errorf("finish idd: target branch %q is the idd worktree branch", target)
	}

	// Set before the first yield below, so no dispatch slips through, and only
	// lifted if the finish fails: the flow is then open for work again.
	ma.state.Finishing = true
	// Captured here rather than around the drain: a sub-task can reach a
	// terminal status while the merge below runs, having landed work on the
	// branch after that merge's diff was taken, and the reconciliation merge
	// must still happen. No sub-task can appear after this point, since
	// dispatch is refused from here on.
	subtasksWereInFlight := len(pendingSubtaskFlowIds(ma.state)) > 0 || ma.state.InFlightSubtaskRunners > 0
	defer func() {
		if err != nil {
			ma.state.Finishing = false
		}
	}()

	if _, commitErr := commitIntent(dCtx, ma.input.Title, true); commitErr != nil {
		return fmt.Errorf("failed to commit pending intent before merge: %w", commitErr)
	}

	if err := ma.mergeIntoTarget(dCtx, target); err != nil {
		return err
	}

	if err := cancelPendingSubtasks(dCtx, ma.state); err != nil {
		return err
	}
	if subtasksWereInFlight {
		if err := ma.mergeIntoTarget(dCtx, target); err != nil {
			return err
		}
	}

	if err := workflow.ExecuteActivity(dCtx, git.CleanupWorktreeActivity, *dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "IDD flow finished").Get(dCtx, nil); err != nil {
		return fmt.Errorf("failed to cleanup the idd worktree after merging into %s: %w", target, err)
	}

	return nil
}

// mergeIntoTarget captures what the idd branch adds to the target, against the
// base pinned before the first merge, and merges it in. Merging a branch that
// is already merged is a no-op, so this is safe to repeat to pick up work that
// landed since.
func (ma *iddMergeApproval) mergeIntoTarget(dCtx DevContext, target string) error {
	if ma.mergeBase == "" {
		base, err := resolveCommit(dCtx, target)
		if err != nil {
			return fmt.Errorf("failed to resolve %s before merging into it: %w", target, err)
		}
		ma.mergeBase = base
	}

	diff, err := GetGitDiff(dCtx, ma.mergeBase, ma.ignoreWhitespace)
	if err != nil {
		return fmt.Errorf("failed to capture the diff being merged into %s: %w", target, err)
	}
	ma.mergedDiff = diff
	ma.params.Diff = diff

	var mergeResult git.MergeActivityResult
	err = workflow.ExecuteActivity(dCtx, git.GitMergeActivity, *dCtx.EnvContainer, git.GitMergeParams{
		SourceBranch:  dCtx.Worktree.Name,
		TargetBranch:  target,
		CommitMessage: fmt.Sprintf("Finish IDD: %s", ma.input.Title),
		MergeStrategy: git.MergeStrategyMerge,
	}).Get(dCtx, &mergeResult)
	if err != nil {
		return fmt.Errorf("failed to merge idd worktree into %s: %w", target, err)
	}
	if mergeResult.HasConflicts {
		return fmt.Errorf("merge conflicts encountered while finishing idd into %s; resolve them manually", target)
	}

	return nil
}

// resolveCommit returns the commit a ref currently points at.
func resolveCommit(dCtx DevContext, ref string) (string, error) {
	var output env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"rev-parse", ref},
	}).Get(dCtx, &output)
	if err != nil {
		return "", err
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("git rev-parse %s failed: %s", ref, strings.TrimSpace(output.Stderr))
	}
	commit := strings.TrimSpace(output.Stdout)
	if commit == "" {
		return "", fmt.Errorf("git rev-parse %s returned no commit", ref)
	}
	return commit, nil
}

// diffBaseRef is what diffs are generated against: the pinned pre-merge tip
// once a finish has merged, so previously merged work stays visible, and the
// selected target branch until then.
func (ma *iddMergeApproval) diffBaseRef() string {
	if ma.mergeBase != "" {
		return ma.mergeBase
	}
	return ma.targetBranch
}

// applyResponseOptions records target branch and diff option changes from a
// response, reporting whether the shown diff is now stale.
func (ma *iddMergeApproval) applyResponseOptions(dCtx DevContext, response flow_action.UserResponse) bool {
	changed := false
	if target, ok := response.Params["targetBranch"].(string); ok {
		if target = strings.TrimSpace(target); target != "" && target != ma.targetBranch {
			ma.targetBranch = target
			dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
			// The pinned base belongs to the branch merged into earlier, so
			// what would be merged into this one is measured from scratch.
			ma.mergeBase = ""
			changed = true
		}
	}
	if ignoreWhitespace, ok := response.Params["ignoreWhitespace"].(bool); ok && ignoreWhitespace != ma.ignoreWhitespace {
		ma.ignoreWhitespace = ignoreWhitespace
		changed = true
	}
	return changed
}

// refreshDiff regenerates the diff of everything that would be merged into the
// currently selected target branch. The previous diff is kept on failure so the
// canvas doesn't lose what it was showing, and the failure is surfaced on the
// request rather than leaving a stale or blank diff unexplained.
func (ma *iddMergeApproval) refreshDiff(dCtx DevContext) {
	diff, err := GetGitDiff(dCtx, ma.diffBaseRef(), ma.ignoreWhitespace)
	if err != nil {
		workflow.GetLogger(dCtx).Warn("Failed to regenerate idd merge approval diff", "Error", err)
		ma.mergeError = fmt.Sprintf("failed to generate diff against %s: %v", ma.targetBranch, err)
		return
	}
	ma.mergeError = ""
	ma.params.Diff = diff
}

// persistPending writes the request's current params and re-opens it, which is
// how the canvas learns a rejected or failed finish still awaits the user.
func (ma *iddMergeApproval) persistPending(dCtx DevContext, flowAction *domain.FlowAction) {
	flowAction.ActionParams = ma.actionParams()
	flowAction.ActionStatus = domain.ActionStatusPending
	flowAction.ActionResult = ""
	var fa *flow_action.FlowActivities
	storageCtx := utils.WithStorageActivityOptions(dCtx)
	if err := workflow.ExecuteActivity(storageCtx, fa.PersistFlowAction, *flowAction).Get(storageCtx, nil); err != nil {
		workflow.GetLogger(dCtx).Error("Failed to update idd merge approval flow action", "Error", err)
	}
}

func (ma *iddMergeApproval) actionParams() map[string]any {
	ma.req.RequestParams["mergeApprovalInfo"] = ma.params
	if ma.mergeError == "" {
		delete(ma.req.RequestParams, iddMergeErrorParam)
	} else {
		ma.req.RequestParams[iddMergeErrorParam] = ma.mergeError
	}
	return ma.req.ActionParams()
}
