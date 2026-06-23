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
  - intent: "#idd-instructions-for-coding-agents"
    code:
      - dev/prompts/author_edit_block/idd_instructions.mustache
      - dev/edit_code.go:renderAuthorEditBlockInitialPrompt
      - dev/prompts/intent/requirements.mustache
      - dev/intent_requirements.go:renderIntentRequirements
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

- Doesn't ask for task description in the UI to start. In fact, it removes
  task fields other than model config, title (required for idd, unlike
  other flow types), and start branch.
      
### Interface

- Starting an idd task/flow creates a new worktree and takes you
  directly to the intent canvas to edit intent files in that worktree
- The canvas is a custom interface for specifying intent, with a simple
  markdown editor + filetree browser.
    - If no intent files exists, shows a prompt to create a new intent file
          in a intent/ directory, prompting for the file name.
    - Remember which intent file was last open and open it again when the specific idd flow id is next accessed
    - If intent files exist, on startup, do not open one: allow the user to select one themselves first, with a prompt telling them to do so
     - .generated files are shown last in filetree

#### Styling

- Editor is full-width between the sidebars and touching the file path header (no gaps)
- Sidebars can be resized and minimized
- Side view for sub tasks can be resized too
     
#### Markdown Editor Component

  - Separate component from intent canvas
  - Uses monaco or codemirror, whatever is easier to get all our desired
          features working in for now.
  - Remembers which file was open last
  - Saves intent automatically as you type in the worktree
  - Intent that is not yet committed has a special background color.
      - This must use a diff algorithm that finds word-level changes
        even in multi-line markdown with newlines shifting around. Words              right beside each other on the same line are merged so there is no            divider between multiple words added in terms of this styling
      - After a commit happens, the this styling goes away for those words (only new words that aren't yet committed show the style)
  - Tab character expands to 2 spaces and does NOT switch to a different interface element as regular browser interactions work, but instead works like an editor is expected to
  - YAML frontmatter is collapsed by default (all other sections expanded)

##### Editor Styling

  - Theme of editor respects dark/light mode 
  - Highlighting text uses a very transparent version of the primary color
  - Strong colors to highlight markdown syntax like the "#" character for headings or the "-" character, etc
- YAML syntax highlighting for the frontmatter
- The caret is the same shape when collapsed or expanded, just rotated about it's centerpoint.


#### Right Sidebar

- The canvas also has a right sidebar that supports showing the user:
  - A button to start implementing the current intent state
  - A list of subtasks
      - Clicking them opens the existing flow view component (not iframe, and         without header/editor links/sidebar nav/etc.), but in a side view
        that can be dismissed, so the intent canvas is always visible.
      - Sub task statuses are shown: completed, failed, in progress, blocked and canceled
  - Any questions wrt highly ambiguous or contradictory intent that have
    surfaced. Note that some level of ambiguity is expected, but not when
    a different answer than the one assumed would require a near-100%
    rewrite of the implementation.

  - Sub tasks become blocked just like top-level flows do, when they wait on user request. normally this is something the task workflow handles, but the idd workflow has to handle it in this case
  - For pending user actions/user requests, the user can decide to unblock these at their convenience. They are shown alongside the sub task that triggered it.
  - There is a button to finish the idd flow. Uses a dismissable side view to show the finish UI
  - Bottom right has a dev run play button, which will start a dev run

### Finish UI

- UI where you can select the branch to merge back into (default: start branch), and shows you the diff that will be merged, and lets you confirm.
- Diff is displayed using existing unified diff viewer, with unexpanded files by default
- Branch selector also uses the standard component for selecting a branch
- Finishing means that the idd flow:
  - Merges into the selected target branch
  - Cleans up its worktree
  - Cancels sub tasks that are still going
  - And sends a completion signal
  - The temporal ends immediately after sending the completion signal
  - The flow status is updated to complete
  - The parent task is marked completed too

### Starting intent sub tasks
    
- Pressing the button to start results in saving and git committing the
      current intent state and creating a sub task for implementing it
- cmd/ctrl+enter to start a new intent sub task from the canvas. does *not* add a newline in the editor if the editor is focused.

### Sub tasks
   
- Uses basic dev flow type with determine requirements disabled
- Makes a worktree based off of HEAD of the worktree for the idd flow
- Automatically gets merged into the idd worktree when completed (new basic
      dev / planned dev workflow option)
    

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

Then we add a text block like this for the requirements prompt for a new intent
file:

````md
Implement the following initial intent:

```md
$ git show {{commit}}
{{diff}}
```
````

When updating intent, it simply starts with "Implement the following intent
update:", but is otherwise the same.