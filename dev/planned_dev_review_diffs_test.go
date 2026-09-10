package dev

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
)

// plannedStepOutcome is what a plan step's review showed: its first auto-review
// round sees everything done since the step started, its second only what
// changed since that first round, and the merge review the planned flow runs
// once its plan is executed sees everything done since the base branch.
type plannedStepOutcome struct {
	fullDiff      string
	sinceDiff     string
	mergeApproval MergeApprovalParams
}

// runPlannedDevStep drives one plan step through the production step loop and
// then through the merge review that follows plan execution. The step's first
// auto-review round rejects the work, so a second coding round runs and is
// reviewed against what the first round already showed.
func (h *reviewDiffsFlowHarness) runPlannedDevStep(requirements string) plannedStepOutcome {
	h.t.Helper()
	h.unfulfilledRounds = 1
	h.roundHooks = map[int]func(t *testing.T, repos *flowRepos){
		1: h.prepare,
		2: h.respond,
	}

	step := DevStep{
		StepNumber:         "1",
		Title:              "Do the work",
		Definition:         requirements,
		Type:               "edit",
		CompletionAnalysis: "The work is done.",
	}
	planExecution := DevPlanExecution{
		Plan:           &DevPlan{Steps: []DevStep{step}},
		StepExecutions: initializeStepExecutions([]DevStep{step}),
	}

	target := "main"
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		dCtx := h.devContext(ctx)
		dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
		result, err := completeDevStepSubflow(dCtx, requirements, planExecution, step)
		if err != nil {
			return err
		}
		if !result.Successful {
			return fmt.Errorf("step did not complete: %s", result.Summary)
		}
		return reviewAndResolve(dCtx, MergeWithReviewParams{
			Requirements: requirements,
			StartBranch:  &target,
		})
	}, &realFlowUserResponder{rejectionMessage: "please address the feedback", approveAfter: 0})

	require.Equal(h.t, 2, h.codingRounds, "the rejected first round must be followed by a second one")
	require.Len(h.t, h.fulfillmentDiffs, 2, "each coding round must have been auto-reviewed once")
	require.NotEmpty(h.t, h.mergeApprovals, "plan execution must be followed by a merge review")

	return plannedStepOutcome{
		fullDiff:      h.fulfillmentDiffs[0],
		sinceDiff:     h.fulfillmentDiffs[1],
		mergeApproval: h.mergeApprovals[len(h.mergeApprovals)-1],
	}
}

// runPlannedDevPlan drives plan execution itself, so each step is pinned where
// production pins it: at the commit that step starts from, which for later steps
// is after earlier steps have committed their work. Each step is accepted at its
// first auto-review round, so every step contributes exactly one review diff.
func (h *reviewDiffsFlowHarness) runPlannedDevPlan(requirements string, stepWork ...func(t *testing.T, repos *flowRepos)) []string {
	h.t.Helper()
	h.roundHooks = map[int]func(t *testing.T, repos *flowRepos){}
	steps := make([]DevStep, 0, len(stepWork))
	for i, work := range stepWork {
		steps = append(steps, DevStep{
			StepNumber:         fmt.Sprintf("%d", i+1),
			Title:              fmt.Sprintf("Step %d", i+1),
			Definition:         requirements,
			Type:               "edit",
			CompletionAnalysis: "The step's work is done.",
		})
		h.roundHooks[i+1] = work
	}

	target := "main"
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		dCtx := h.devContext(ctx)
		dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
		_, err := followDevPlanSubflow(dCtx, FollowDevPlanInput{
			WorkspaceId:  dCtx.WorkspaceId,
			EnvContainer: h.envContainer,
			Requirements: requirements,
			DevPlan:      &DevPlan{Steps: steps},
		})
		return err
	}, &realFlowUserResponder{rejectionMessage: "please address the feedback", approveAfter: 0})

	require.Equal(h.t, len(stepWork), h.codingRounds, "every step must have run its work")
	require.Len(h.t, h.fulfillmentDiffs, len(stepWork), "every step must have been auto-reviewed once")
	return h.fulfillmentDiffs
}

// TestPlannedDevPlanPinsEachStepWhereItStarted drives plan execution through the
// production path, where each step is pinned in turn, to show that a step is
// reviewed on its own work: what an earlier step committed is behind the later
// step's start point.
func TestPlannedDevPlanPinsEachStepWhereItStarted(t *testing.T) {
	t.Parallel()

	h := newReviewDiffsFlowHarness(t)
	stepDiffs := h.runPlannedDevPlan("do the work",
		func(t *testing.T, repos *flowRepos) {
			repos.work.commit("first_step.txt", "FIRST_STEP_WORK\n", "first step work")
		},
		func(t *testing.T, repos *flowRepos) {
			repos.work.commit("second_step.txt", "SECOND_STEP_WORK\n", "second step work")
		},
	)

	assert.Contains(t, stepDiffs[0], "FIRST_STEP_WORK")
	assert.Contains(t, stepDiffs[1], "SECOND_STEP_WORK")
	assert.NotContains(t, stepDiffs[1], "FIRST_STEP_WORK",
		"a step is reviewed from where it started, so an earlier step's committed work is not part of it")
}

// TestPlannedDevStepDiffsRealFlow covers what a plan step's review rounds show.
// A step is reviewed the same way a whole task is, except that it starts where
// the step did rather than at the base branch: work committed before the step
// belongs to an earlier step and is none of this step's business, while the
// merge review that follows the plan still covers all of it.
func TestPlannedDevStepDiffsRealFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		beforeStep func(t *testing.T, repos *flowRepos)
		prepare    func(t *testing.T, repos *flowRepos)
		respond    func(t *testing.T, repos *flowRepos)
		assert     func(t *testing.T, outcome plannedStepOutcome, work *repoMutator)
	}{
		{
			name: "auto-reviewer and merge approval judge the same diff",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("reviewed.txt", "REVIEWED_WORK\n", "the work under review")
			},
			assert: func(t *testing.T, outcome plannedStepOutcome, work *repoMutator) {
				assert.Contains(t, outcome.fullDiff, "REVIEWED_WORK")
				assert.Equal(t, normalizeReviewDiff(outcome.fullDiff), normalizeReviewDiff(outcome.sinceDiff),
					"auto-review retries must still review the unchanged work")
				assert.Equal(t, normalizeReviewDiff(outcome.fullDiff), normalizeReviewDiff(outcome.mergeApproval.Diff),
					"the step's work is the whole task's work, so both reviews judge the same diff")
			},
		},
		{
			name: "staged changes are always included",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("staged_first.txt", "FIRST_ROUND_STAGED\n")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("staged_second.txt", "SECOND_ROUND_STAGED\n")
			},
			assert: func(t *testing.T, outcome plannedStepOutcome, work *repoMutator) {
				assert.Contains(t, outcome.fullDiff, "FIRST_ROUND_STAGED")
				assert.Contains(t, outcome.sinceDiff, "SECOND_ROUND_STAGED")
				assert.Contains(t, outcome.sinceDiff, "FIRST_ROUND_STAGED",
					"auto-review does not establish a user-review baseline")
				assert.Contains(t, outcome.mergeApproval.Diff, "FIRST_ROUND_STAGED")
				assert.Contains(t, outcome.mergeApproval.Diff, "SECOND_ROUND_STAGED")
			},
		},
		{
			name: "step commits are included but earlier steps' commits are not",
			beforeStep: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("earlier_step.txt", "PREVIOUS_STEP_WORK\n", "previous step work")
			},
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("committed_first.txt", "FIRST_ROUND_COMMIT\n", "first round work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("committed_second.txt", "SECOND_ROUND_COMMIT\n", "second round work")
			},
			assert: func(t *testing.T, outcome plannedStepOutcome, work *repoMutator) {
				assert.Equal(t, 3, work.commits, "each round of work and the previous step's work are committed")
				assert.Contains(t, outcome.fullDiff, "FIRST_ROUND_COMMIT")
				assert.NotContains(t, outcome.fullDiff, "PREVIOUS_STEP_WORK",
					"work committed before the step started belongs to an earlier step")
				assert.Contains(t, outcome.sinceDiff, "SECOND_ROUND_COMMIT")
				assert.Contains(t, outcome.sinceDiff, "FIRST_ROUND_COMMIT")
				assert.NotContains(t, outcome.sinceDiff, "PREVIOUS_STEP_WORK")
				assert.Contains(t, outcome.mergeApproval.Diff, "PREVIOUS_STEP_WORK",
					"the merge review covers the whole task, not just this step")
				assert.Contains(t, outcome.mergeApproval.Diff, "SECOND_ROUND_COMMIT")
			},
		},
		{
			name: "clean base branch merge affects neither diff",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("ours.txt", "OUR_WORK\n", "our work")
				repos.commitUpstream("upstream.txt", "UNRELATED_BASE_CHANGE\n", "upstream work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				result := repos.work.mergeBase()
				require.False(t, result.HasConflicts, "the upstream change must merge cleanly")
			},
			assert: func(t *testing.T, outcome plannedStepOutcome, work *repoMutator) {
				assert.Equal(t, 1, work.merges, "the base branch must have been merged in")
				assert.Contains(t, outcome.fullDiff, "OUR_WORK")
				assert.NotContains(t, outcome.fullDiff, "UNRELATED_BASE_CHANGE")
				assert.Equal(t, normalizeReviewDiff(outcome.fullDiff), normalizeReviewDiff(outcome.sinceDiff),
					"a clean base merge leaves the step's work unchanged")
				assert.Equal(t, normalizeReviewDiff(outcome.fullDiff), normalizeReviewDiff(outcome.mergeApproval.Diff),
					"the step's work is all the task did, so both reviews show the same diff")
			},
		},
		{
			name: "conflict resolution shows our changes only",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("shared.txt", "line one\nOUR_CHANGE\nline three\n", "our change")
				repos.commitUpstream("shared.txt", "line one\nTHEIR_CHANGE\nline three\n", "their change")
				repos.commitUpstream("other.txt", "UNRELATED_BASE_CHANGE\n", "unrelated upstream work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				result := repos.work.mergeBase()
				require.True(t, result.HasConflicts, "the upstream change must conflict with ours")
				repos.work.commit("shared.txt", "line one\nRESOLVED_CHANGE\nline three\n", "resolve conflict")
			},
			assert: func(t *testing.T, outcome plannedStepOutcome, work *repoMutator) {
				assert.Equal(t, 1, work.merges, "the base branch must have been merged in")
				assert.Contains(t, outcome.fullDiff, "OUR_CHANGE")
				assert.Contains(t, outcome.sinceDiff, "RESOLVED_CHANGE",
					"our conflict resolution is our own change")
				assert.NotContains(t, outcome.sinceDiff, "UNRELATED_BASE_CHANGE",
					"non-conflicting base changes are not ours")
				assert.Contains(t, outcome.mergeApproval.Diff, "RESOLVED_CHANGE")
				assert.NotContains(t, outcome.mergeApproval.Diff, "UNRELATED_BASE_CHANGE")
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newReviewDiffsFlowHarness(t)
			h.prepare = tc.prepare
			h.respond = tc.respond
			if tc.beforeStep != nil {
				tc.beforeStep(t, h.repos)
			}

			outcome := h.runPlannedDevStep("do the work")

			assert.Empty(t, h.mergeApprovals[0].DiffSinceLastReview,
				"the first merge review has no prior review to diff against")
			tc.assert(t, outcome, h.repos.work)
		})
	}
}
