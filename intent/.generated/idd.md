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
      - frontend/src/components/BranchSelector.vue
  - intent: "#standalone-markdown-editor-component"
    code:
      - frontend/src/components/IntentMarkdownEditor.vue
      - frontend/src/views/IntentCanvasView.vue
  - intent: "#idle-markdown-auto-formatting"
    code:
      - frontend/src/components/IntentMarkdownEditor.vue
      - frontend/src/lib/markdown_format.ts
  - intent: "#uncommitted-intent-baseline-comes-from-head"
    code:
      - frontend/src/lib/intent_diff.ts
      - frontend/src/lib/intent_diff_editor.ts
      - frontend/src/views/IntentCanvasView.vue
  - intent: "#resizable-and-minimizable-canvas-layout"
    code:
      - frontend/src/views/IntentCanvasView.vue
  - intent: "#right-sidebar"
    code:
      - frontend/src/views/IntentCanvasView.vue
      - dev/idd_workflow.go:IddSubtask
      - dev/idd_workflow.go:setSubtaskStatus
      - dev/idd_workflow.go:runIntentSubtask
  - intent: "#background-orchestrator-agent"
    code:
      - dev/idd_orchestrator.go
      - dev/idd_orchestrator.go:runIddOrchestratorTurn
      - dev/idd_orchestrator.go:startIntentSubtaskTool
      - dev/idd_orchestrator.go:addNudgeTool
      - dev/idd_workflow.go:IddState
      - dev/idd_workflow.go:IddNudge
      - dev/idd_workflow.go:SetIddAutoModeSignal
      - dev/idd_workflow.go:RunIddOrchestratorSignal
      - dev/idd_workflow.go:IddWorkflow
      - dev/manage_chat_history.go
      - api/intent_api.go:SetIddAutoModeHandler
      - api/intent_api.go:RunIddOrchestratorHandler
      - frontend/src/views/IntentCanvasView.vue
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
merge target branch using the shared `BranchSelector` component (defaulting to
the idd worktree's start branch, exposed via the `idd_state` query) and
confirms after reviewing the diff that would be merged (`git diff
<target>...HEAD` from the worktree). Confirmation signals the long-running IDD
workflow, which first cancels any still in-flight sub-tasks (in_progress or
blocked) and waits for them to settle so their auto-merges into the worktree
branch don't race the finish-merge, then commits any pending intent, merges
its branch into the chosen target, cleans up the worktree, and exits cleanly
so the parent task workflow marks the IDD task completed.

## Uncommitted intent baseline comes from HEAD

To distinguish committed vs uncommitted intent in the canvas, the read handler
returns the file's content at `HEAD` alongside the working-copy content. The
frontend diffs them at word granularity (whitespace runs compared loosely so
newline shifts don't register as edits) and decorates the added ranges in the
CodeMirror editor. After a sub-task is started (which commits the current
intent), the canvas re-reads the committed baseline for the active file so the
uncommitted-highlight styling clears immediately without waiting for the user
to refresh or switch files.

## Standalone markdown editor component

The CodeMirror-based intent editor lives in its own component
(`IntentMarkdownEditor.vue`) so the intent canvas view can stay focused on file
listing, save orchestration, and IDD sub-task UI. The component owns its
EditorView lifecycle, dark-mode reconfiguration, uncommitted-range highlight,
tab-to-spaces handling (configurable indent size, defaulting to 2), and the
default-collapsed frontmatter fold (the whole section collapses onto the first
line, including the closing `---`). It communicates via `v-model` for the
working-copy text, a `committedContent` prop for the diff baseline, and a
`shortcut-submit` event so the host view can decide what Mod-Enter does (start
sub-task on the canvas).

## Background orchestrator agent

The background orchestrator's auto sub-task creation is a user-toggled mode on
the IDD canvas's right rail. The actual edit-idle heuristic ("a chunk of intent
has settled — evaluate it") lives in the frontend: after 30s of editor
inactivity following any edits, the canvas posts a single
`run_orchestrator` request to the workflow. Keeping the heuristic on the
client avoids workflow-side timers and replay churn.

When a run is requested, the IDD workflow runs one LLM-driven decision turn
(`runIddOrchestratorTurn`) that sees the pending intent diff and the
existing sub-tasks/nudges, then chooses between two tools:
`start_intent_subtask` (optionally scoped to a chunk of intent via a
free-form `prompt`) and `add_nudge` (a short, non-blocking thought surfaced
on the canvas — never a question demanding an answer, just food for thought
about ambiguous/contradictory intent). A turn that decides to wait simply
emits no tool call. The agent runs regardless of the AutoMode toggle so
nudges keep surfacing for ambiguous intent; AutoMode only restricts the
per-turn tool list (and a safety guard in the start-tool branch) so that
when auto sub-task creation is off, only `add_nudge` is available and any
rogue `start_intent_subtask` call is rejected with an explanatory
tool-result. The orchestrator's chat history persists across turns
for the workflow's lifetime so the agent recalls which intent chunks it
already dispatched; trimming via `manageChatHistoryV2` preserves every
`start_intent_subtask` assistant message (and its matching tool result) via
the `IntentTaskStart` context type, so the dispatch ledger never gets lost
to compaction. Nudge dedup is text-based and lives in `IddState.Nudges`.

Toggling auto-mode is a separate signal so the workflow can ignore stale run
requests without round-tripping through the frontend, and so the user can
disable the agent at any time without affecting in-flight sub-tasks.
Newly started IDD workflows default `AutoMode` to true (gated by the
`idd-auto-mode-default-on` workflow version so replays of pre-existing
histories keep the original off-by-default semantics and replay
deterministically); existing flows continue to honour whatever the user
last toggled.

For local worktree environments the IDD workflow also runs a long-lived
edit-watcher activity (gated by the `idd-edit-watcher` version) that
returns when the worktree has been quiet for a short idle window after at
least one intent-file edit, so the orchestrator gets a steady server-side
trigger that does not depend on the canvas being open. Remote env types
still rely on the frontend idle heuristic — closing the parity gap for
remote envs requires sandbox-side FS watching plumbing that is out of
scope here. Both trigger paths feed the same capacity-1 buffered channel
drained by a single coroutine, so concurrent triggers coalesce into at
most one pending turn and the orchestrator never races itself.

Sub-task dispatch from a tool call is fire-and-forget via `workflow.Go`:
`runIntentSubtask` blocks on its child workflow's completion (potentially
many minutes), so invoking it inline from the orchestrator drainer would
freeze every subsequent turn until that sub-task finishes. The
fire-and-forget shape mirrors the user-signal handler in `IddWorkflow`
and keeps each turn (and the drainer) snappy.

Partial-scope sub-tasks pass their scoping intent through the
`start_intent_subtask` tool's free-form `prompt` argument: the agent can
name a section heading from an intent file, pick out a paragraph, or
describe any other narrow focus in plain language, and that string flows
directly into the sub-task's requirements text. A dedicated structured
field for "intent file path + section heading" was considered and
rejected because the intent explicitly lists free-form prompt as one of
the allowed scope shapes, and the existing prompt already accommodates
both file-section references and arbitrary narrowing instructions.

The intent diff the orchestrator sees each turn is computed against the
IDD flow's start branch (`state.DefaultTargetBranch`), not the worktree
`HEAD`. Every sub-task dispatch commits the *entire* current intent
worktree to HEAD even when the dispatch is partial-scope, so a
HEAD-relative diff would hide every other un-dispatched slice from the
next turn. Diffing against the start branch keeps the full intent body
visible across turns; the orchestrator avoids re-dispatching slices by
consulting the existing-sub-tasks summary included in each turn prompt.
This change is gated by the `idd-orchestrator-diff-from-start` workflow
version so pre-existing histories replay against the original HEAD-based
diff.

The existing-sub-tasks summary embeds the *dispatched intent diff* of
each still-in-flight sub-task (capped per-sub-task to keep the prompt
bounded) alongside its scope prompt, status, and short commit. Without
this, whole-scope sub-tasks — where `ScopePrompt` is empty by design —
left the orchestrator with only `flowId@commit[status]` to reason about,
which is not enough to recognize that a freshly-arriving slice of intent
overlaps something already in flight. The dispatched diff is stored on
`IddSubtask.DispatchedDiff` with `json:"-"` so it stays out of the
canvas query response (the same information is reachable from the
sub-task's own flow view) and only ever appears in the orchestrator's
internal turn prompt. Completed/failed/canceled sub-tasks drop their
dispatched-diff snippet from the prompt since they no longer constrain
fresh dispatches.

## Resizable and minimizable canvas layout

The intent canvas uses a CSS-grid layout whose left/right sidebar widths are
driven by CSS variables bound to reactive widths, with drag handles overlaid at
the column boundaries so users can resize either rail; each rail also has a
minimize toggle that collapses it to a thin strip exposing only an expand
button. The sub-task side panel similarly has a left-edge drag handle and a
variable-driven width. All widths and minimized states persist to
`localStorage` (single shared key across flows since they're layout
preferences, not per-flow content). The markdown editor itself is full-width
between the rails and flush with the file-path header — no internal padding,
border, or max-width — so the writing surface dominates the canvas.

## Side view escape dismissal

The sub-task side panel is a focusable container (`tabindex="-1"`) that grabs
focus when opened, so pressing Escape while it has focus dismisses it via a
`keydown.esc.stop` handler on the panel itself rather than a global listener.
This keeps the intent canvas's own keyboard shortcuts unaffected when the side
view isn't focused.

## Idle markdown auto-formatting

After the editor sits idle for 15 seconds following a user edit, the intent
markdown editor reflows plain prose paragraphs and collapses runs of blank
lines into single blank lines. Structural content -- YAML frontmatter, fenced
code blocks, headings, list items, blockquotes, tables, horizontal rules, and
indented code -- is left untouched so auto-formatting can never silently
rewrite executable or structured content. External `modelValue` replacements
cancel any pending idle timer so freshly-loaded content isn't reformatted on
open.

## Sub-task list grouping and collapse

The right-sidebar sub-task list is grouped by status -- blocked first, then
in-progress, then everything else -- and within each group ordered by last
update (falling back to creation time) newest-first. The list is a scrollable
section, and completed sub-tasks whose last update is over an hour old are
folded into a collapsible "N Completed" entry with a caret toggle so a long
history of finished work doesn't crowd out active sub-tasks. Sub-tasks carry
`createdAt`/`updatedAt` timestamps (set from the workflow clock when launched
and on every status change) to drive this ordering and the staleness cutoff.
