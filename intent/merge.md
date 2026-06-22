# Merge/Review Process

TODO: Specify entire process as intent. Incremental updates are shown here only.

## Conflicts

When a conflict is detected on the base/primary worktree, then merge is aborted, and the conflict is recreated by merging the base into own worktree. Then the conflict is resolved by invoking the edit code subflow, with a specific prompt injected that is very similar to review feedback (including showing previous review feedback), but with the latest "feedback" being to resolve the merge conflicts cleanly without losing any functionality on either the.

At the end of this, we check if all conflicts are resolved (loop if not), then re-run tests (loop if any fail). Then a special diff mechanism (specialized to show the conflict resolution itself - regular invocation of git diff is empty in this case and cached diff only shows selected side) is extracted and used for criteria fulfillment (loop if it says there's an issue). At some point here, the merge commit is finalized, likely after all the loops we should do so programmatically (we tell the agent not to commit). Finally, we merge back into the base/primary worktree, and are done.

Note that this entire process also applies when the merge is done in the flow-owned worktree, just without the extra steps to back out a merge and re-merge to base worktree.