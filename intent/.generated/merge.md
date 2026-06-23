---
intent_links:
  - intent: "#conflicts"
    code:
      - dev/basic_dev_workflow.go:mergeWorktreeIfApproved
      - dev/resolve_merge_conflicts.go:resolveMergeConflictsSubflow
      - dev/resolve_merge_conflicts.go:resolveMergeConflictsLoop
      - dev/resolve_merge_conflicts.go:recreateConflictOnOwnWorktree
      - dev/resolve_merge_conflicts.go:finalizeMergeCommit
      - dev/resolve_merge_conflicts.go:checkConflictResolutionFulfillment
      - dev/prompt_info.go:ConflictResolutionInfo
      - dev/edit_code.go:renderConflictResolutionPrompt
      - dev/prompts/conflict_resolution/initial.mustache
      - coding/git/git_conflict_resolution.go:GitMergeAbortActivity
      - coding/git/git_conflict_resolution.go:GitMergeInProgressActivity
      - coding/git/git_conflict_resolution.go:GitListUnmergedActivity
      - coding/git/git_conflict_resolution.go:GitSnapshotConflictMarkersActivity
      - coding/git/git_conflict_resolution.go:GitConflictResolutionDiffActivity
      - coding/git/git_conflict_resolution.go:GitCommitMergeActivity
      - coding/git/git_conflict_resolution.go:GitMergeIntoWorktreeActivity
      - dev/basic_dev_workflow.go:MergeWithReviewParams
      - worker/worker.go:StartWorker
      - scripts/run_activity/main.go:buildActivityRegistry
---
# Inferred Merge/Review Implementation Notes

> Generated/inferred intent. Trusted less than human-authored intent; it records
> consequential, high-level inferences only and is not the source of truth.

## Conflicts

Conflict resolution v3 is gated by the workflow version
`conflict-resolution-v3`. For new executions, when `GitMergeActivity` reports
conflicts:

- Conflicts that landed on the target-branch worktree are aborted there and
  recreated on the flow's own worktree by merging the target branch in via
  `GitMergeIntoWorktreeActivity`, so the resolver always operates on the
  flow-owned worktree regardless of where the conflict was first observed.
- A `ConflictMarkerSnapshot` (a temporary tree built from the unmerged index
  entries) is captured before any edits. The temp index file lives under
  `git rev-parse --git-path …` rather than a literal `.git/…` path so it
  works in linked worktrees (where `.git` is a file). Diffing that snapshot
  against the working tree later yields the conflict-resolution diff used
  for verification, which would otherwise be empty under regular `git diff`
  once markers are removed.
- The `resolveMergeConflictsSubflow` then drives an `EditCode` loop using the
  new `ConflictResolutionInfo` prompt type. The resolver prompt instructs
  the agent to first `sed`-rename raw conflict markers to
  `CONFLICT_START` / `CONFLICT_DIVIDER` / `CONFLICT_END` placeholders so
  that subsequent edit blocks can safely quote those lines without their
  SEARCH/REPLACE sections being mis-parsed by the edit-block extractor
  (whose delimiters share the `<<<<<<<` / `=======` / `>>>>>>>` shape).
  After each pass it checks both the unmerged index (`git ls-files -u`)
  AND a tracked-file grep for literal `<<<<<<<`/`=======`/`>>>>>>>`
  markers (loop if either is non-empty, so a faux-resolve that `git
  add`ed a still-marked file is caught), re-runs configured tests (loop
  on failure), and runs
  `CheckIfCriteriaFulfilled` with the snapshot-based diff as `Work` (loop
  on negative fulfillment). The resolver is explicitly told not to commit;
  the orchestrator finalizes the merge commit via `GitCommitMergeActivity`,
  which stages with `git add -A` so newly created/renamed files in the
  resolution are captured too. Finalization first calls
  `GitMergeInProgressActivity` so it no-ops when `MERGE_HEAD` is absent
  (e.g. the conflict recreation merge actually fast-forwarded cleanly),
  avoiding a spurious single-parent commit on top of HEAD.
- Accumulated reviewer feedback is forwarded into
  `ResolveMergeConflictsParams.PreviousReview` via a new field on
  `MergeWithReviewParams`, populated by `reviewAndResolve` from its
  `reviewMessages` slice. Both the resolver prompt and the criteria
  fulfillment check include this so the resolver doesn't undo edits made
  specifically to satisfy reviewers.
- After the merge commit is finalized on the flow's own worktree, a final
  `GitMergeActivity` fast-forwards the target branch onto source. This step
  is correct as a fast-forward because target is now an ancestor of source
  (the target tip is the second parent of the resolution merge commit). The
  final merge always uses the regular `merge` strategy, overriding any
  squash request, so the resolution commit itself is preserved on the
  target branch — squashing would collapse it away and lose the explicit
  record of how the conflict was resolved.

Pre-v3 executions retain the legacy human-in-the-loop `GetUserContinue`
fallback so in-flight workflows replay deterministically.