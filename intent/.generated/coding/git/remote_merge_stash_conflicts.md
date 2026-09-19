---
intent_links:
  - intent: "#solution"
    code:
      - coding/git/merge_coordinator.go:MergeCoordinator
      - coding/git/merge_coordinator.go:mergeRepository
      - coding/git/merge_transport.go:mergeTransport
      - coding/git/git_merge.go:GitMergeActivity
      - coding/git/git_merge.go:GitTransferWorktreeChangesActivity
      - coding/git/merge_resolution.go:relocateStashConflict
      - coding/git/merge_resolution.go:returnResolution
---

# Remote Merge and Stash Conflicts

## Problem

A merge can succeed in a remote clone but fail during sync-back because the
host target worktree has uncommitted edits. Remote merges need the same
stash → merge → restore → conflict-resolution lifecycle as local merges,
without losing user edits.

## Solution

- **Source** is the flow-owned worktree; **target** is the destination branch's
  worktree. For remote merges, an existing host target worktree is authoritative:
  the source branch is backed up there and merged there, avoiding a second
  sync-back merge. Without one, the existing fallback remains.
- **Merge coordinator** owns this lifecycle inside the existing activities:
  preserving target edits, merging, restoring them, and returning resolved edits.
  It does not replace the workflow's conflict-resolution agent.
- **Repository context** couples paths to their execution environment.
  **Transport** discovers the host repository and supplies Git objects across
  clones; within one repository, copying is unnecessary. Checkout and stash
  decisions remain in the coordinator, not `env`.
- **Resolution** means Git conflict resolution. Ordinary branch conflicts are
  recreated on the source for resolution. Stash conflicts arise when restoring
  target edits after a successful merge; these also move to the source.
  `ReturnResolution` carries the resolved edits back as **uncommitted changes**,
  not another merge commit.
- Owned stash SHAs and durable Git refs preserve edits and record progress across
  retries. Cleanup follows confirmed delivery; intervening edits and ambiguous
  destination ownership cause errors rather than destructive guesses.

See [merge conflict intent](../../../merge.md#conflicts) for workflow behavior.