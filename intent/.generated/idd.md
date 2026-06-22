---
intent_links:
  - intent: "#long-lived-idd-workflow"
    code:
      - dev/idd_workflow.go:IddWorkflow
      - dev/idd_workflow.go:setSubtaskStatus
  - intent: "#clarifications-from-sub-tasks"
    code:
      - dev/idd_workflow.go:IddWorkflow
      - dev/idd_workflow.go:runIntentSubtask
      - dev/task_workflow.go:TaskWorkflow
  - intent: "#auto-merge-targets-the-start-branch"
    code:
      - dev/basic_dev_workflow.go:BasicDevOptions
      - dev/basic_dev_workflow.go:mergeWorktreeIfApproved
---
# Inferred IDD Implementation Notes

> Generated/inferred intent. Trusted less than human-authored intent; it records
> consequential, high-level inferences only and is not the source of truth.

## Long-lived IDD workflow

The IDD workflow is long-running: after worktree setup it loops on a selector
handling signals to start sub-tasks and to record sub-task closures, so a single
intent worktree can spawn many sub-tasks over its lifetime.

## Clarifications from sub-tasks

When intent is too ambiguous or contradictory to proceed, a sub-task raises a
user request and blocks waiting for the answer. This blocking is intentional: the
user must clarify or edit intent to unblock it (the agent may later decide a
subsequent intent update resolves the ambiguity). The IDD workflow forwards that
request up to the task workflow, whose existing handler surfaces it as a pending
user request and marks the IDD task blocked. Sub-task flows are parented to the
IDD task (not the IDD flow), so when the user answers, the response routes
directly back to the originating sub-task to unblock it and the completion
handler returns the IDD task to in-progress.

## Auto-merge targets the start branch

Sub-task worktrees auto-merge back into their start branch (the idd worktree
branch) on completion, reusing the existing start branch rather than a separate
merge-target option.