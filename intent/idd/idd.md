---
intent_links:
  - intent: "#seamless-intent-driven-development-flow"
    code:
      - domain/flow.go:FlowTypeIdd
      - dev/idd_workflow.go:IddWorkflow
      - worker/worker.go:RegisterWorkflows
  - intent: "#context"
    code:
      - dev/prompts/author_edit_block/idd_instructions.mustache
  - intent: "#mvp"
    code:
      - domain/flow.go:FlowTypeIdd
      - dev/task_workflow.go:TaskWorkflow
      - dev/dev_agent_manager_workflow.go:executeWorkRequest
      - dev/idd_workflow.go:IddWorkflow
      - dev/idd_workflow.go:runIntentSubtask
      - dev/idd_workflow.go:commitIntent
      - dev/basic_dev_workflow.go:BasicDevOptions
      - dev/intent_requirements.go:renderIntentRequirements
      - api/intent_api.go:ListIntentFilesHandler
      - api/intent_api.go:ReadIntentFileHandler
      - api/intent_api.go:WriteIntentFileHandler
      - api/intent_api.go:StartIntentSubtaskHandler
      - api/intent_api.go:committedIntentContent
      - frontend/src/components/TaskModal.vue
      - frontend/src/views/IntentCanvasView.vue
      - frontend/src/router/index.ts
      - frontend/src/lib/intent_diff.ts
      - frontend/src/lib/intent_diff_editor.ts
  - intent: "#markdown-editor-component"
    code:
      - frontend/src/components/IntentMarkdownEditor.vue
      - frontend/src/lib/markdown_format.ts
  - intent: "#sub-tasks"
    code:
      - api/intent_api.go:StartIntentSubtaskHandler
      - dev/idd_workflow.go:runIntentSubtask
  - intent: "#idd-instructions-for-coding-agents"
    code:
      - dev/prompts/author_edit_block/idd_instructions.mustache
      - dev/edit_code.go:renderAuthorEditBlockInitialPrompt
      - dev/prompts/intent/requirements.mustache
      - dev/intent_requirements.go:renderIntentRequirements
  - intent: "#background-orchestrator-agent"
    code:
      - dev/idd_workflow.go:IddState
      - dev/idd_workflow.go:SetIddAutoModeSignal
      - dev/idd_workflow.go:RunIddOrchestratorSignal
      - dev/idd_workflow.go:IddWorkflow
      - dev/idd_workflow.go:runIntentSubtask
      - dev/idd_workflow.go:subtaskTerminalNotice
      - dev/idd_watcher_activity.go:IddWatchEditIdleActivity
      - dev/idd_orchestrator.go:pendingIntentDiff
      - dev/idd_orchestrator.go:runIddOrchestratorTurn
      - coding/git/git_diff.go:DiffUntrackedFilesActivity
      - dev/manage_chat_history.go
      - api/intent_api.go:SetIddAutoModeHandler
      - api/intent_api.go:RunIddOrchestratorHandler
      - frontend/src/views/IntentCanvasView.vue
      - dev/idd_orchestrator.go:StartIntentSubtaskToolArgs
      - dev/idd_orchestrator.go:resolveSubtaskScope
      - dev/idd_workflow.go:StartIntentSubtaskSignal
      - dev/intent_requirements.go:renderIntentRequirements
      - dev/prompts/intent/requirements_prompt_only.mustache
---
# Seamless Intent Driven Development Flow

Intent Driven Development (IDD) is a new developmental methodology where
software engineers focus on intent rather than code as they build software. This
new flow type makes IDD seamless and effortless, by providing a custom interface
that feels magical.

## Context

Intent refers primarily to specifications that are checked in to markdown files
in the repository, but also to knowledge/context that can be used to infer
intent. Executable specifications, like tests, are the most important type
intent. Natural language intent instead requires an LLM to interpret and manage
and is inherently less reliable.

Importantly, intent is human-authored, not AI-authored. Inferred intent can be
made explicit, but this is clearly distinguished from human intent, and is
treated as less reliable. Code is also treated as less reliable than intent,
except where the intent is underspecified.

If intent is actively at odds with the code, the intent is typically the source
of truth. If changes have been made to the code more recently without updating
intent and they are in conflict, we will want to update the intent to match the
code, or the code to match the intent, depending on which is more correct
according to the human author/user.

## MVP

### New Flow Type: Intent Driven Development (idd)

- Doesn't ask for task description in the UI to start. In fact, it removes task
  fields other than model config, title (required for idd, unlike other flow
  types), and start branch.

### Interface

- Starting an idd task/flow creates a new worktree and takes you directly to the
  intent canvas to edit intent files in that worktree
- The canvas is a custom interface for specifying intent, with a simple markdown
  editor + filetree browser.
  - If no intent files exists, shows a prompt to create a new intent file in a
    intent/ directory, prompting for the file name.
  - Remember which intent file was last open and open it again when the specific
    idd flow id is next accessed
  - If intent files exist, on startup, do not open one: allow the user to select
    one themselves first, with a prompt telling them to do so
  - .generated files are shown last in filetree

#### Styling

- Editor is full-width between the sidebars and touching the file path header
  (no gaps) and the bottom of the viewport.
  - The editor's line numbers + line content scroll independently of the
    sidebars
  - The scrolling content keeps going until the *last* line of the content is
    displayed at the top of the editor window
  - Disable scroller "bouncing" animation when scrolling fast past bounds,
    simply stop the content from scrolling at its bounds as soon as the bounds
    are hit.
- Sidebars can be resized and minimized
- Side view for sub tasks can be resized too

#### Markdown Editor Component

  - Separate component from intent canvas
  - Uses monaco or codemirror, whatever is easier to get all our desired
    features working in for now.
  - Remembers which file was open last
  - Intent that is not yet committed has a special background color.
      - This must use a diff algorithm that finds word-level changes even in
        multi-line markdown with newlines shifting around. Words right beside
        each other on the same line are merged so there is no divider between
        multiple words added in terms of this styling
      - After a commit happens and a sub task is started, then this styling goes
        away for those words immediately, not requiring a refresh (only new
        words that aren't yet committed show the style)
  - Tab character expands to 2 spaces and does NOT switch to a different
    interface element as regular browser interactions work, but instead works
    like an editor is expected to
  - When text is selected, tab indents it and shift+tab dedents it
  - YAML frontmatter is always collapsed by default in the editor (all other
    sections are expanded by default)
    - Rather than the YAML being collapsed, the entire frontmatter section is
      collapsed on the first line of the markdown file
  - Saves intent automatically as you type in the worktree
  - Auto-formats markdown on save
    - Collapses whitespace
    - Removes trailing whitespace except in code blocks
    - Wraps lines in paragraphs & list items
    - Empty list items are left alone
    - Tests confirm all scenarios
  - Auto-formatting & saving minimize disruption to the editor state:
    - The cursor and selection state remains where it was exactly
      - Never change characters to the left of the cursor on the active line
    - The vertical position of the cursor relative to the viewport is maintained
      exactly
    - Tests confirm these scenarios

##### Editor Styling

- Theme of editor respects dark/light mode
- Highlighting text uses a very transparent version of the primary color
- Strong colors to highlight markdown syntax like the "#" character for headings
  or the "-" character, etc
- YAML syntax highlighting for the frontmatter
- The caret is the same shape when collapsed or expanded, just rotated about
  it's centerpoint.

##### Right Sidebar

- The canvas also has a right sidebar that supports showing the user:
  - The sidebar itself isn't scrollable, but items within it may be
  - A button to [start a sub task] to implement the current intent state
    - Uses standard keyboard shortcut component
  - A list of subtasks
      - Clicking them opens the existing flow view component (not iframe, and
        without own header/sidebar nav/etc.), but in a side view. Includes
        editor links rendered on top of the sub task header, offset to avoid
        dismiss button
      - When the side view has focus, pressing escape dismisses it. that can be
        dismissed, so the intent canvas is always visible.
      - Sub task statuses are shown: completed, failed, in progress, blocked and
        canceled
      - Grouped by status, with blocked at the top, then in progress, then
        others
      - Within group, ordered by last updated (fallback: created), reverse
        chronological
      - Subtask list section is a scrollable section that extends until the dev
        run at the bottom
      - When many have completed such that scrolling is required, the ones done
        over 1 hour ago are collapsed under "[caret] N Completed"
  - Any questions wrt highly ambiguous or contradictory intent that have
    surfaced, either by sub tasks or the orchestrator agent. Note that some
    level of ambiguity is expected, but not when a different answer than the one
    assumed would require a near-100% rewrite of the implementation.

  - Sub tasks become blocked just like top-level flows do, when they wait on
    user request. normally this is something the task workflow handles, but the
    idd workflow has to handle it in this case
  - For pending user actions/user requests, the user can decide to unblock these
    at their convenience. They are shown alongside the sub task that triggered
    it.
  - There is a button to finish the idd flow. Uses a dismissable side view to
    show the finish UI
  - Stuck to the bottom of the sidebar is section with a play button, which will
    start a dev run.
  - There is some way to interact with the orchestrator agent that normally just
    runs in the background. Opens a side view with a new component. Allows
    viewing the full history of what happened with the agent as well as queuing
    messages to it.

#### Sub task component

- Has an mini-button with icon to cancel that displays on hover (similar to task
  cards)
  - After canceling, the sub task status changes to canceled.

#### Finish UI

- UI where you can select the branch to merge back into (default: start branch),
  and shows you the diff that will be merged, and lets you confirm.
- Diff is displayed using existing unified diff viewer, with unexpanded files by
  default
- Branch selector also uses the standard component for selecting a branch
- Finishing means that the idd flow:
  - Merges into the selected target branch
  - Cleans up its worktree
  - Cancels sub tasks that are still going
  - And sends a completion signal
  - The temporal ends immediately after sending the completion signal
  - The flow status is updated to complete
  - The parent task is marked completed too
- Any errors in the IDD workflow after this point are prominently displayed in
  the finish panel. Any pending user requests are also surfaced if any.
  - The logic is very similar to that in the FlowView/SubflowContainer.
- After finishing successfully, the user is redirected to the kanban board

### Start a sub task

Pressing the button to start immediately results in these actions:

1. Formatting all markdown files with changes and then immediately saving
2. Git adding the intent files with modifications
3. Creating a git commit and recording its sha at creation time
4. Using the created sha & diff to create a title for the sub task
5. Start a sub task to implement the change

- cmd/ctrl+I to start a new intent sub task from the canvas. does *not* add a
  newline in the editor if the editor is focused.
- A new background orchestrator AI agent watches edits as they are made in
  chunks (some heuristic to ensure LLM isn't invoked for partial edits, and
  always invoked if there are unproceesed edits and there has been no activity
  for a while)

### Background Orchestrator Agent

- This agent is implemented by the IDD flow (which includes this agent along
  with other orchestration for IDD)
- IDD flow checks on intent updates via fsnotify and a long-running activity.
  - The activity has a heartbeat to detect failures, and infinite retries and
    infinite timeout.
  - It returns the latest diff of changes
  - This diff works for both existing tracked files, and new (untracked) files
    (the diff is akin to the full intent content in this case). Existing git
    diff helpers/activities are reused for this.
- This AI agent automatically starts tasks if it thinks there is a good chunk of
  intent to work on
- Task start tool calls and responses are always retained when managing chat
  history.
- It can scope the intent to a specific section of an intent file, or the entire
  intent diff, or just an arbitrary prompt that scopes and directs the subtask
  to a portion of the intent (not providing it the entire intent diff in this
  case). In the latter situation, the orchestrator is responsible for ensuring
  that there are tasks for every aspect of the diff. A final cleanup subtask to
  ensure the entire intent file is done is acceptable.
- Titles provided for subtasks must be very short, and not repeat "Implement" or
  "Update" similar fluff words, just describes the part of intent it's related
  to in 2-3 words. These titles are always viewed in the context of an intent
  file, which provides a lot of disambiguating context already.
- The agent makes sure to either provide very strong interfaces between the
  subtasks such that they can operate in parallel, or it serializes execution to
  ensure.
- When subtasks complete, the orchestrator is notified and can take an action,
  such as invoking the next subtask, if it wants to.

### Sub tasks
- When manually created, uses planned dev flow type with determine requirements disabled
- Makes a worktree based off of HEAD of the worktree for the idd flow
- Automatically gets merged into the idd worktree when completed (new basic dev
  / planned dev workflow option)
- User requests go to the parent flow, which is the idd flow in this case, which
  passes them on to the task workflow for now.
- Parent task remains blocked as long as any sub task is blocked

## IDD Instructions for Coding Agents

Verbatim system instructions to be included in the prompt to coding agents:

```md
IMPORTANT: When needed, refer to the `intent/` directory for the mission/goals,
product requirements, technical design, UX design (wireframes), any more
detailed specifications, other constraints, and contextual knowledge.

`intent/.generated` holds intent that is inferred or generated by an LLM. It's
trusted less than non-generated intent; you must use it to record any intent you
infer, but can not treat it as the source of truth. Ensure this intent is highly
concise, containing only consequential and high-level intent inferences rather
than every minor detail that will show up in the code itself.

LLMs are NOT ALLOWED to author/edit intent/* files (exception: intent links in
the frontmatter are meant to be LLM-edited). They may author/edit
intent/.generated/* files. Non-generated intent files are purely human-authored
other than intent links.

Intent markdown files can carry YAML frontmatter, notably an `intent_links`
field: a list of entries linking intent sections to code symbols/files, each
with an `intent` selector and a `code` list. A code entry's symbol is optional
and indicates a link to a full file. The `intent` selector is a slugified
heading href (e.g. this section's is `#intent`):

    intent_links:
      - intent: "#some-heading"
        code:
            - main.go:main

Add these links as code changes are made, to keep intent and code in sync. Do
this for both human-authored and LLM-generated intent files.

The human-authored intent files are the source of truth, above the LLM-generated
ones and even above the code itself. Anything LLM-generated, like code is the
last source for understanding requirements: it's generated by coding agents and
may contain AI hallucinations or mistakes, so any decisions encoded there are
suspect and may not match the actual intent — missing features, behaving
differently than intended, or being (necessarily) over-specified in its behavior
versus what we actually require. Intent always overrules code; if the intent
needs changes, the user must make them, and the code should then follow.

If in doubt — e.g. the latest user request conflicts with the intent — ask the
user to clarify their intent.
```

Then we obtain a "clean diff", which ignores whitespace and renders word-level
diffs at best effort (ideally matching the quality of results of our frontend
in-editor diffing). We use the clean diff in an additional text block like this,
making it the requirements prompt. For a new intent file, the prompt looks like
this:

`````
The following new intent file has already been committed to {{{ path/to/intent/file.md }}} (shown below for reference). Your job is to update the code so that the system's behavior matches the new intent. Do NOT re-edit the intent markdown file itself - treat the diff as a specification that the code must now conform to fully. It may be underspecified, but no code should be in contradiction with the intent. Infer intent as well as you can where it is underspecified.

```sh
$ git show {{{commit}}}
{{{clean_diff}}}
```

Identify which behaviors in the diff are newly required or changed, locate the
corresponding code (frontend, backend, prompts, etc.), and make the code changes
needed. If a change is purely editorial (whitespace, wording with no semantic
effect), no code change is needed for that hunk.
`````

And like this when the file already existed but has an update:

````
The following intent update has already been committed to {{{ path/to/intent/file.md }}}
(shown below for reference). Your job is to update the code so that the
system's behavior matches the new intent. Do NOT re-edit the intent markdown
file itself — treat the diff as a specification change that the code must now
conform to.

```sh
$ git show {{{commit}}}
{{{clean_diff}}}
```

Identify which behaviors in the diff are newly required or changed, locate the
corresponding code (frontend, backend, prompts, etc.), and make the code changes
needed. If a change is purely editorial (whitespace, formatting or wording with no semantic
effect), no code change is needed for that hunk. Just 
`````
