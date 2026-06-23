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
      - dev/idd_workflow.go:setSubtaskStatus
      - dev/task_workflow.go:TaskWorkflow
      - flow_action/user_interaction.go:GetUserResponse
      - flow_action/user_interaction.go:SubtaskUnblocked
  - intent: "#auto-merge-targets-the-start-branch"
    code:
      - dev/basic_dev_workflow.go:BasicDevOptions
      - dev/basic_dev_workflow.go:mergeWorktreeIfApproved
  - intent: "#finishing-idd-via-confirmed-merge"
    code:
      - dev/idd_workflow.go:IddWorkflow
      - dev/idd_workflow.go:FinishIddSignal
      - dev/idd_workflow.go:finishIdd
      - dev/idd_workflow.go:cancelPendingSubtasks
      - api/intent_api.go:ListIntentBranchesHandler
      - api/intent_api.go:FinishIntentDiffHandler
      - api/intent_api.go:FinishIntentHandler
      - frontend/src/views/IntentCanvasView.vue
  - intent: "#standalone-markdown-editor-component"
    code:
      - frontend/src/components/IntentMarkdownEditor.vue
      - frontend/src/views/IntentCanvasView.vue
  - intent: "#uncommitted-intent-baseline-comes-from-head"
    code:
      - frontend/src/lib/intent_diff.ts
      - frontend/src/lib/intent_diff_editor.ts
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
user request and marks the IDD task blocked. The IDD workflow also marks the
originating sub-task as "blocked" in its in-memory state so the canvas can show
that status alongside completed/failed/in-progress/canceled — top-level flows
get their blocked status from the task workflow, but sub-tasks have no separate
task workflow, so the IDD workflow tracks it directly. When the user answers,
the response routes directly back to the originating sub-task to unblock it; on
unblock the sub-task signals the IDD workflow (SubtaskUnblocked) so the canvas
returns the sub-task to in-progress, and the completion handler returns the IDD
task itself to in-progress.

## Auto-merge targets the start branch

Sub-task worktrees auto-merge back into their start branch (the idd worktree
branch) on completion, reusing the existing start branch rather than a separate
merge-target option.

## Finishing IDD via confirmed merge

A "Finish IDD" button on the canvas opens a dialog where the user picks the
merge target branch (defaulting to the idd worktree's start branch, exposed via
the `idd_state` query) and confirms after reviewing the diff that would be
merged (`git diff <target>...HEAD` from the worktree). Confirmation signals the
long-running IDD workflow, which first cancels any still in-flight sub-tasks
(in_progress or blocked) and waits for them to settle so their auto-merges into
the worktree branch don't race the finish-merge, then commits any pending
intent, merges its branch into the chosen target, cleans up the worktree, and
exits cleanly so the parent task workflow marks the IDD task completed.

## Uncommitted intent baseline comes from HEAD

To distinguish committed vs uncommitted intent in the canvas, the read handler
returns the file's content at `HEAD` alongside the working-copy content. The
frontend diffs them at word granularity (whitespace runs compared loosely so
newline shifts don't register as edits) and decorates the added ranges in the
CodeMirror editor.

## Standalone markdown editor component

The CodeMirror-based intent editor lives in its own component
(`IntentMarkdownEditor.vue`) so the intent canvas view can stay focused on file
listing, save orchestration, and IDD sub-task UI. The component owns its
EditorView lifecycle, dark-mode reconfiguration, uncommitted-range highlight,
tab-to-spaces handling (configurable indent size, defaulting to 2), and the
default-collapsed YAML frontmatter fold. It communicates via `v-model` for the
working-copy text, a `committedContent` prop for the diff baseline, and a
`shortcut-submit` event so the host view can decide what Mod-Enter does (start
sub-task on the canvas).