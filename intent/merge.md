# Merge/Review Process

TODO: Specify entire process as intent. Incremental updates are shown here only.

## Conflicts

When a conflict is detected on the base/primary worktree, and the failure caused
by conflicts or dirty base worktree is not considered a failure that can be
retried (whether automatically, or via user-controlled retry). Automatic and
user-controlled retry is for other unknown failures.

If the failure to merge is caused by a dirty base/primary worktree, then we
temporarily stash, merge, unstash and auto-resolve conflicts (see below) with
the stash if any.

If merge failure is caused by a merge conflict, then merge is aborted, and the
conflict is recreated by merging the base into own worktree. Then the conflict
is resolved by invoking the edit code subflow, with a specific prompt injected
that is very similar to review feedback (including showing previous review
feedback), but with the latest "feedback" being to resolve the merge conflicts
cleanly without losing any functionality on either side.

At the end of this, we check if all conflicts are resolved (loop if not), then
re-run tests (loop if any fail). Then a special diff mechanism (specialized to
show the conflict resolution itself - regular invocation of git diff is empty in
this case and cached diff only shows selected side) is extracted and used for
criteria fulfillment (loop if it says there's an issue). At some point here, the
merge commit is finalized, likely after all the loops we should do so
programmatically (we tell the agent not to commit). Finally, we merge back into
the base/primary worktree, and are done.

Note that this entire process also applies when the merge is done in the
flow-owned worktree, just without the extra steps to back out a merge and
re-merge to base worktree.

Due to how edit blocks work, the prompt for resolving conflicts must mention
using sed to replace conflict markers with CONFLICT_START/DIVIDER/END, so that
the conflict markers aren't confused with the edit block format delimiters.