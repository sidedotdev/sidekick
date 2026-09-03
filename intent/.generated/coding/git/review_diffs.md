---
intent_links:
  - intent: "#review-diff-generation"
    code:
      - coding/review_diffs_activity.go:GenerateReviewDiffsActivity
      - coding/review_diffs_activity.go:GenerateReviewDiffsParams
      - coding/review_diffs_activity.go:GenerateReviewDiffsResult
      - coding/review_diffs_activity.go:fullReviewDiff
      - coding/diffanalysis/interdiff.go:Interdiff
      - coding/git/git_diff.go:GitDiffActivity
      - coding/git/git_merge_base.go:MergeBase
  - intent: "#start-points"
    code:
      - coding/review_diffs_activity.go:fullReviewDiff
      - coding/review_diffs_activity.go:diffAtStartPoint
      - coding/review_diffs_activity_test.go:reviewDiffsScenarios
      - coding/git/git_rev_parse.go:GitRevParseActivity
      - dev/follow_dev_plan.go:stepReviewState
      - dev/follow_dev_plan.go:pinStepReviewState
  - intent: "#activity-level-cases"
    code:
      - coding/review_diffs_activity_test.go:TestGenerateReviewDiffsActivity_FirstRound
      - coding/review_diffs_activity_test.go:TestGenerateReviewDiffsActivity_StagedChangesAlwaysIncluded
      - coding/review_diffs_activity_test.go:TestGenerateReviewDiffsActivity_CommitsRelativeToStartPointAndReview
      - coding/review_diffs_activity_test.go:TestGenerateReviewDiffsActivity_CleanBaseMergeDoesNotAffectDiffs
      - coding/review_diffs_activity_test.go:TestGenerateReviewDiffsActivity_ConflictResolvingBaseMerge
  - intent: "#flow-level-cases"
    code:
      - dev/basic_dev_workflow.go:getMergeApproval
      - dev/basic_dev_workflow.go:reviewAndResolve
      - dev/fulfillment.go:CheckWorkMeetsCriteria
      - dev/fulfillment.go:CheckWorkMeetsCriteriaWithDiff
      - dev/follow_dev_plan.go:checkIfDevStepCompleted
      - dev/follow_dev_plan.go:completeDevStepSubflow
      - dev/step_start_pin_test.go:StepStartPinTestSuite
      - dev/planned_dev_review_diffs_test.go:TestPlannedDevStepDiffsRealFlow
      - dev/planned_dev_review_diffs_test.go:TestPlannedDevPlanPinsEachStepWhereItStarted
      - coding/git/git_conflict_resolution.go:GitConflictResolutionDiffActivity
  - intent: "#degradation"
    code:
      - coding/review_diffs_activity.go:GenerateReviewDiffsActivity
      - coding/diffanalysis/interdiff.go:Interdiff
---
# Inferred Review Diffs Intent

> Generated/inferred intent. Trusted less than human-authored intent; it records
> consequential, high-level inferences only and is not the source of truth.

## Review Diff Generation

Every review round needs two diffs, and they are produced together, from the
same git state, by a single activity:

- the full diff of our own changes since a start point
- the diff of what changed since the last review, which is an in-process
  interdiff between the diff shown at the last review and the current full diff

Neither diff depends on a git object that only the review mechanism references,
so garbage collection can never invalidate a review round.

The full diff is a plain three-dot plus staged plus unmerged diff. It never
picks between candidate diffs by length or by any other heuristic: earlier
review-diff mechanisms did so and were wrong in ways this intent exists to rule
out.

## Start Points

Two kinds of start points exist, and they mean the same thing to a reviewer:
"changes that are ours, since here".

- A base branch start point: the diff is taken three-dot, so changes merged in
  from that branch are naturally excluded, because merging advances the merge
  base.
- A pinned commit start point (the state at the beginning of a planned dev
  step): the pinned commit stays an ancestor of HEAD, so it excludes nothing on
  its own. Our changes since it are therefore derived by subtracting the work
  that already existed at the pinned commit from the current base branch diff.

Consequently, for both kinds of start point:

- Staged changes always appear in both the full and the since diff.
- Commits made on the worktree appear in both diffs, except that commits made
  before the last review are absent from the since diff.
- Changes committed before the start point appear in neither diff.
- Purely merging the base branch, with no conflicts to resolve, leaves both
  diffs unchanged.
- When merging the base branch required resolving conflicts, our end of the
  resolution appears in both diffs, while non-conflicting changes coming from
  the base branch do not.

## Activity Level Cases

The above matrix is exercised against real git, for both start point kinds,
with no mocked commands, on diffs small enough that diff summarization never
comes into play.

The first review round, having no prior review diff, shows only the full diff.

## Flow Level Cases

Each flow carries the prior review's diff text forward in workflow state, and
gets the same diff semantics:

- Basic dev: start point is the base branch.
- Review/resolve: start point is the base branch; the auto-reviewer sees the
  full diff from before the user's feedback plus the since diff of the changes
  made in response to it.
- Planned dev: start point is pinned to the state at the beginning of the step,
  so work committed by earlier steps is in neither diff.
- Conflict resolution: the resolution diff covers the conflict regions only,
  so hunks unrelated to the conflict are not compared.

Across all flows, the diff handed to the criteria-fulfillment auto-reviewer is
the same diff handed to the human merge approval process, where the human sees
both the full and the since diff and one of them matches.

## Degradation

Review diff generation fails only when git itself fails. Interdiff trouble,
including diffs that cannot be aligned or malformed input, degrades to a
best-effort delta or to the full diff, so a review round is never blocked by
diffing the diffs. Diff generation failures are not user-retryable: the
auto-reviewer falls back to the full diff, and merge approval shows a failure
note where the since-review section would be.