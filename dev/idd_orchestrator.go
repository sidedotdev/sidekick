package dev

import (
	"encoding/json"
	"fmt"
	"strings"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

// IntentSubtaskScopeWhole means the orchestrator launched a sub-task to
// implement the entire current intent diff.
const IntentSubtaskScopeWhole = "whole"

// IntentSubtaskScopeSection means the orchestrator launched a sub-task whose
// prompt names a specific section of an intent file; the sub-task still
// receives the full intent diff for context.
const IntentSubtaskScopeSection = "section"

// IntentSubtaskScopePrompt means the orchestrator launched a sub-task scoped
// by an arbitrary free-form prompt; the sub-task receives only that prompt,
// not the full intent diff, so the orchestrator remains responsible for
// following up on intent the prompt does not cover.
const IntentSubtaskScopePrompt = "prompt"

// IntentSubtaskScopePartial is the legacy scope value from before the
// section/prompt split; it is still accepted (for replays of older histories)
// and gets section semantics.
const IntentSubtaskScopePartial = "partial"

// resolveSubtaskScope normalizes a start_intent_subtask scope argument.
// Unrecognized values (including the legacy 'partial') get section semantics:
// a scope prompt alongside the full intent diff, matching pre-split behavior.
func resolveSubtaskScope(rawScope string) string {
	switch rawScope {
	case IntentSubtaskScopeWhole, IntentSubtaskScopeSection, IntentSubtaskScopePrompt:
		return rawScope
	default:
		return IntentSubtaskScopeSection
	}
}

// StartIntentSubtaskToolArgs is the LLM-facing schema for the
// start_intent_subtask tool the IDD orchestrator agent can call. The
// 'section' scope targets a named section of an intent file while still
// giving the sub-task the full intent diff; the 'prompt' scope directs the
// sub-task with only an arbitrary free-form prompt, deliberately omitting
// the intent diff from its requirements.
type StartIntentSubtaskToolArgs struct {
	Scope   string `json:"scope" jsonschema:"description=How to scope the sub-task: 'whole' implements the full pending intent diff\\, 'section' implements just the intent-file section named in 'prompt' (the sub-task still sees the full intent diff)\\, 'prompt' implements an arbitrary free-form scope described in 'prompt' (the sub-task sees only that prompt\\, not the intent diff).,enum=whole,enum=section,enum=prompt"`
	Prompt  string `json:"prompt,omitempty" jsonschema:"description=Required when scope is 'section' or 'prompt'. For 'section'\\, name the section heading (e.g. \"only the '### Finish UI' section of intent/idd/idd.md\"). For 'prompt'\\, describe the narrow focus in free-form language\\, self-contained enough to direct the sub-task without the intent diff. Ignored when scope is 'whole'."`
	Planned bool   `json:"planned,omitempty" jsonschema:"description=Set true to run the sub-task as a planned-dev flow\\, which builds an explicit multi-step plan before coding. Prefer this for larger sub-tasks that span disparate changes or multiple areas of the codebase. Leave false (the default) for focused sub-tasks a single coding pass can handle."`
}

// AddNudgeToolArgs is the LLM-facing schema for the add_nudge tool. Nudges
// are pithy, non-blocking food-for-thought the orchestrator surfaces about
// the current intent — questions worth the human reconsidering, not exact
// answers.
type AddNudgeToolArgs struct {
	Text       string `json:"text" jsonschema:"description=One short sentence (max ~120 chars) flagging something to reconsider. Phrase as a prompt to think\\, not a full question with context."`
	AnchorText string `json:"anchorText,omitempty" jsonschema:"description=Optional verbatim snippet from the intent files this nudge relates to. Used for future hover-highlight UX; safe to omit."`
}

var startIntentSubtaskTool = llm.Tool{
	Name:        "start_intent_subtask",
	Description: "Launch a sub-task that implements the current intent (or a scoped chunk of it). Only call this when there is a coherent, load-bearing chunk of intent ready to implement; otherwise wait.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&StartIntentSubtaskToolArgs{}),
}

var addNudgeTool = llm.Tool{
	Name:        "add_nudge",
	Description: "Surface a short, non-blocking thought to the human author about the current intent. Use sparingly: only for things worth reconsidering before more is written. Keep nudges pithy.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&AddNudgeToolArgs{}),
}

const iddOrchestratorSystemPrompt = `You are a background orchestrator agent watching a human author edit an intent document for software development.

The intent diff you receive each turn covers the entire intent body authored in this IDD flow (diffed against the IDD start branch). Slices already dispatched to in-flight or completed sub-tasks remain in that diff; the existing-sub-tasks list tells you which slices are already in flight so you can avoid re-dispatching them.

Your job, every time intent settles after a burst of edits, is to decide:

1. Is there a coherent, load-bearing chunk of intent — newly added, or already present but not yet dispatched — that is now ready to be implemented as a sub-task? If yes, call start_intent_subtask. You have three ways to scope the sub-task:
   (a) scope='whole' — implement the entire intent diff. Only valid for the very first dispatch in this IDD flow when no prior sub-tasks exist and the diff is small and cohesive.
   (b) scope='section' with a prompt naming a specific section of an intent file (e.g. "implement only the '### Finish UI' section of intent/idd/idd.md"). The sub-task receives the full intent diff alongside your prompt.
   (c) scope='prompt' with an arbitrary free-form prompt that directs the sub-task to a portion of the intent without naming a heading. The sub-task receives ONLY your prompt — not the intent diff — so make it self-contained, and you are responsible for following up on any intent it does not cover.
   Prefer (b) or (c) over (a). Once any sub-task exists, always use section or prompt scoping so each sub-task targets just the slice it owns.
   Also decide how the sub-task should run: set planned=true to have it build an explicit multi-step plan before coding, which is preferred for larger sub-tasks that span disparate changes or multiple areas of the codebase. Leave planned=false (the default) for focused sub-tasks a single coding pass can handle.

2. Is there anything in the current intent that looks highly ambiguous, contradictory, or load-bearing-but-underspecified — where a wrong assumption would cause a near-total rewrite of the implementation? If yes, call add_nudge once for each such concern.

Otherwise, output a brief assistant message explaining why you are taking no action this turn, and do not call any tool.

Rules:
- Be terse. Only act when intent is genuinely ready.
- Do not ask the human for answers; nudges are prompts to think, not requests for replies.
- Do not re-dispatch the same chunk of intent you already dispatched in earlier turns. The history of prior start_intent_subtask calls is preserved verbatim above, and the existing-sub-tasks list reflects current in-flight slices.
- Do not nudge about the same concern more than once.`

const iddOrchestratorTurnPromptTemplate = `Intent edits have settled. Current state:

Full intent diff (relative to the IDD start branch — covers everything authored in this IDD flow so far, including intent already dispatched to in-flight sub-tasks):
%s

Existing sub-tasks: %s
Existing nudges: %s

Decide whether to start a sub-task for an un-dispatched slice of intent, add nudges, or wait. Use the existing-sub-tasks list above to avoid re-dispatching slices already in flight.`

// iddOrchestratorMaxChatLength is the soft upper bound on the orchestrator's
// chat history. ManageChatHistory trims to ~90% of this when exceeded,
// preserving start_intent_subtask tool-call/tool-result pairs via the
// ContextTypeIntentTaskStart marker so the agent always recalls which intent
// chunks it has already dispatched.
const iddOrchestratorMaxChatLength = 100_000

// pendingIntentDiff returns the diff describing the intent body authored in
// this IDD flow, restricted to the intent/ subtree. The diff base is the IDD
// flow's start branch (state.DefaultTargetBranch) rather than HEAD so that a
// partial-scope sub-task dispatch — which commits the entire current intent
// state to HEAD even though only a slice of it is being implemented — does
// not make the remaining un-dispatched intent slices invisible to subsequent
// orchestrator turns. The orchestrator relies on summarizeSubtasksForOrchestrator
// to avoid re-dispatching slices already in flight. Restricting to intent/
// keeps unrelated worktree edits from muddying the orchestrator's view.
func pendingIntentDiff(dCtx DevContext, state *IddState) (string, error) {
	base := "HEAD"
	if workflow.GetVersion(dCtx, "idd-orchestrator-diff-from-start", workflow.DefaultVersion, 1) >= 1 {
		if strings.TrimSpace(state.DefaultTargetBranch) != "" {
			base = state.DefaultTargetBranch
		}
	}
	var out env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"diff", base, "--", "intent"},
	}).Get(dCtx, &out)
	if err != nil {
		return "", fmt.Errorf("failed to compute pending intent diff: %w", err)
	}
	tracked := out.Stdout

	// Brand-new (untracked) intent files never show up in `git diff`, so a
	// freshly authored intent file would otherwise be invisible to the
	// orchestrator. Reuse the shared untracked-diff activity (which renders
	// untracked content as a full add) and scope it to intent/ to match the
	// tracked diff above.
	var untracked string
	if workflow.GetVersion(dCtx, "idd-orchestrator-untracked-intent", workflow.DefaultVersion, 1) >= 1 {
		if err := workflow.ExecuteActivity(dCtx, git.DiffUntrackedFilesActivity, *dCtx.EnvContainer, []string{"intent"}).Get(dCtx, &untracked); err != nil {
			return "", fmt.Errorf("failed to compute untracked intent diff: %w", err)
		}
	}

	parts := make([]string, 0, 2)
	if strings.TrimSpace(tracked) != "" {
		parts = append(parts, tracked)
	}
	if strings.TrimSpace(untracked) != "" {
		parts = append(parts, untracked)
	}
	return strings.Join(parts, "\n"), nil
}

// runIddOrchestratorTurn runs one decision turn of the background
// orchestrator agent. It is a no-op when the pending intent diff is empty
// (no new intent to reason about). When the agent calls start_intent_subtask
// the matching sub-task is launched in a fire-and-forget coroutine so the
// turn — and the serial orchestrator drainer that invokes it — returns
// promptly even though sub-task completion can take many minutes; nudges
// are appended to state synchronously.
//
// nudgeOnly restricts the turn to the add_nudge tool only — used when the
// user has disabled auto sub-task creation but still wants the orchestrator
// to surface clarifications about ambiguous/contradictory intent.
//
// requestOrchestratorTurn is forwarded to runIntentSubtask so a follow-up
// turn is requested when a launched sub-task reaches a terminal status.
func runIddOrchestratorTurn(dCtx DevContext, input IddWorkflowInput, state *IddState, chatHistory *persisted_ai.ChatHistoryContainer, nudgeOnly bool, requestOrchestratorTurn func()) {
	log := workflow.GetLogger(dCtx)

	diff, err := pendingIntentDiff(dCtx, state)
	if err != nil {
		log.Error("Orchestrator: failed to read pending intent diff", "Error", err)
		return
	}
	if strings.TrimSpace(diff) == "" {
		return
	}

	if chatHistory.Len() == 0 {
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
			Role:        llm.ChatMessageRoleSystem,
			Content:     iddOrchestratorSystemPrompt,
			ContextType: ContextTypeInitialInstructions,
		}); err != nil {
			log.Error("Orchestrator: failed to seed system prompt", "Error", err)
			return
		}
	}

	turnPrompt := fmt.Sprintf(iddOrchestratorTurnPromptTemplate,
		diff,
		summarizeSubtasksForOrchestrator(state),
		summarizeNudgesForOrchestrator(state),
	)
	// Notices queued by runIntentSubtask survive until a turn actually runs
	// (the empty-diff early return above leaves them queued), then are
	// delivered exactly once.
	if len(state.PendingSubtaskNotices) > 0 {
		turnPrompt = "Sub-task updates since the last turn:\n- " +
			strings.Join(state.PendingSubtaskNotices, "\n- ") +
			"\n\n" + turnPrompt
		state.PendingSubtaskNotices = nil
	}
	if nudgeOnly {
		turnPrompt += "\n\nNOTE: Auto sub-task creation is disabled by the user this turn. Do NOT call start_intent_subtask; only call add_nudge if there is genuine ambiguity worth surfacing, otherwise stay silent."
	}
	if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
		Role:    llm.ChatMessageRoleUser,
		Content: turnPrompt,
	}); err != nil {
		log.Error("Orchestrator: failed to append turn prompt", "Error", err)
		return
	}

	modelConfig := dCtx.GetModelConfig(common.PlanningKey, 0, "default")
	ManageChatHistory(dCtx.ExecContext, chatHistory, dCtx.WorkspaceId, iddOrchestratorMaxChatLength, modelConfig)

	tools := []*llm.Tool{&addNudgeTool}
	if !nudgeOnly {
		tools = append([]*llm.Tool{&startIntentSubtaskTool}, tools...)
	}
	options := llm2.Options{
		Tools:       tools,
		ToolChoice:  llm.ToolChoice{Type: llm.ToolChoiceTypeAuto},
		ModelConfig: modelConfig,
	}

	chatResponse, err := TrackedToolChat(dCtx, "idd_orchestrator", options, chatHistory)
	if err != nil {
		log.Error("Orchestrator: LLM call failed", "Error", err)
		return
	}
	respMsg := chatResponse.GetMessage()
	toolCalls := respMsg.GetToolCalls()

	contextType := ""
	for _, tc := range toolCalls {
		if tc.Name == startIntentSubtaskTool.Name {
			contextType = ContextTypeIntentTaskStart
			break
		}
	}
	// Re-emit the assistant turn as a llm.ChatMessage so ContextType is
	// preserved through the legacy/llm2 chat-history boundary; the trim
	// logic in manageChatHistoryV2 keys off ContextType to retain
	// start_intent_subtask pairs across history compaction.
	if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
		Role:        llm.ChatMessageRoleAssistant,
		Content:     respMsg.GetContentString(),
		ToolCalls:   toolCalls,
		ContextType: contextType,
	}); err != nil {
		log.Error("Orchestrator: failed to append response", "Error", err)
		return
	}
	if len(toolCalls) == 0 {
		return
	}

	startedAny := false
	for _, tc := range toolCalls {
		switch tc.Name {
		case startIntentSubtaskTool.Name:
			if nudgeOnly {
				if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks("Auto sub-task creation is disabled; start_intent_subtask is unavailable this turn."),
				}); err != nil {
					log.Error("Orchestrator: failed to append nudge-only-start tool result", "Error", err)
				}
				continue
			}
			if startedAny {
				if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks("Only one start_intent_subtask call per turn. Subsequent calls are ignored."),
				}); err != nil {
					log.Error("Orchestrator: failed to append duplicate-start tool result", "Error", err)
				}
				continue
			}
			var args StartIntentSubtaskToolArgs
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				if appendErr := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks("Failed to parse arguments: " + err.Error()),
				}); appendErr != nil {
					log.Error("Orchestrator: failed to append parse-error tool result", "Error", appendErr)
				}
				continue
			}

			scope := resolveSubtaskScope(args.Scope)
			scopePrompt := strings.TrimSpace(args.Prompt)
			if scope != IntentSubtaskScopeWhole && scopePrompt == "" {
				if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks(fmt.Sprintf("scope='%s' requires a non-empty prompt", scope)),
				}); err != nil {
					log.Error("Orchestrator: failed to append empty-prompt tool result", "Error", err)
				}
				continue
			}
			if scope == IntentSubtaskScopeWhole {
				scopePrompt = ""
			}

			// Whether the IDD workflow already has any sub-tasks determines
			// whether this commit is an initial-intent or update commit. The
			// orchestrator only ever fires after some intent has already been
			// committed in practice, but treat the first dispatch (no prior
			// sub-tasks) as the initial intent so the requirements wording
			// matches the user-initiated path.
			update := len(state.Subtasks) > 0
			sig := StartIntentSubtaskSignal{
				Update:      update,
				ScopePrompt: scopePrompt,
				Planned:     args.Planned,
				PromptOnly:  scope == IntentSubtaskScopePrompt,
			}
			// Reserve the sub-task entry synchronously so the very next
			// iteration of this tool-call loop (and any subsequent
			// orchestrator turn that may run before commitIntent yields
			// complete) observes the in-flight sub-task: this is what
			// makes `update := len(state.Subtasks) > 0` above classify
			// concurrent dispatches correctly, and prevents the next turn
			// from re-deciding to dispatch for the same pending diff.
			flowId := reservePendingSubtask(dCtx, state, scopePrompt)
			// runIntentSubtask blocks until the child workflow completes;
			// running it inline here would freeze the orchestrator drainer
			// (and all subsequent turns) until the sub-task finishes,
			// possibly many minutes. Fire-and-forget via workflow.Go
			// mirrors the user-initiated signal path in IddWorkflow.
			workflow.Go(dCtx, func(goCtx workflow.Context) {
				runIntentSubtask(dCtx.WithContext(goCtx), input, sig, state, flowId, requestOrchestratorTurn)
			})
			startedAny = true

			confirmation := fmt.Sprintf("Sub-task launched (scope=%s, planned=%t).", scope, args.Planned)
			if scope != IntentSubtaskScopeWhole {
				confirmation = fmt.Sprintf("Sub-task launched (scope=%s, planned=%t, prompt=%q).", scope, args.Planned, scopePrompt)
			}
			if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				Content:    llm2.TextContentBlocks(confirmation),
			}); err != nil {
				log.Error("Orchestrator: failed to append start confirmation", "Error", err)
			}

		case addNudgeTool.Name:
			var args AddNudgeToolArgs
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				if appendErr := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks("Failed to parse arguments: " + err.Error()),
				}); appendErr != nil {
					log.Error("Orchestrator: failed to append nudge parse-error tool result", "Error", appendErr)
				}
				continue
			}
			text := strings.TrimSpace(args.Text)
			if text == "" {
				if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					IsError:    true,
					Content:    llm2.TextContentBlocks("Nudge text is required"),
				}); err != nil {
					log.Error("Orchestrator: failed to append empty-nudge tool result", "Error", err)
				}
				continue
			}
			if nudgeAlreadySurfaced(state, text) {
				if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
					Name:       tc.Name,
					ToolCallId: tc.Id,
					Content:    llm2.TextContentBlocks("Nudge already surfaced; skipped."),
				}); err != nil {
					log.Error("Orchestrator: failed to append duplicate-nudge tool result", "Error", err)
				}
				continue
			}
			state.Nudges = append(state.Nudges, IddNudge{Text: text, AnchorText: strings.TrimSpace(args.AnchorText)})
			if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				Content:    llm2.TextContentBlocks("Nudge recorded."),
			}); err != nil {
				log.Error("Orchestrator: failed to append nudge confirmation", "Error", err)
			}

		default:
			if err := addToolCallResponse(dCtx.ExecContext, chatHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				IsError:    true,
				Content:    llm2.TextContentBlocks(fmt.Sprintf("Unknown tool %q", tc.Name)),
			}); err != nil {
				log.Error("Orchestrator: failed to append unknown-tool result", "Error", err)
			}
		}
	}
}

// maxDispatchedDiffPerSubtask caps the per-sub-task dispatched-diff snippet
// embedded in the orchestrator's turn prompt. Without a cap a handful of
// large in-flight sub-tasks could blow past the model's context window;
// with one, the orchestrator still sees a deterministic prefix that is
// almost always enough to recognize already-dispatched intent slices.
const maxDispatchedDiffPerSubtask = 2000

func summarizeSubtasksForOrchestrator(state *IddState) string {
	if len(state.Subtasks) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, st := range state.Subtasks {
		fmt.Fprintf(&b, "\n- %s@%s [%s]", st.FlowId, shortCommit(st.Commit), st.Status)
		if st.ScopePrompt != "" {
			fmt.Fprintf(&b, "\n  scope prompt: %s", strings.TrimSpace(st.ScopePrompt))
		}
		// Embed the dispatched intent slice only while the sub-task is still
		// in flight: once it has completed, failed, or been canceled it no
		// longer constrains what fresh intent can be dispatched, and keeping
		// it would just bloat the prompt.
		if isPendingSubtaskStatus(st.Status) {
			snippet := strings.TrimSpace(st.DispatchedDiff)
			if snippet != "" {
				if len(snippet) > maxDispatchedDiffPerSubtask {
					snippet = snippet[:maxDispatchedDiffPerSubtask] + "\n…(truncated)"
				}
				fmt.Fprintf(&b, "\n  dispatched intent diff:\n  ```\n%s\n  ```", snippet)
			}
		}
	}
	return b.String()
}

func summarizeNudgesForOrchestrator(state *IddState) string {
	if len(state.Nudges) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(state.Nudges))
	for _, n := range state.Nudges {
		parts = append(parts, fmt.Sprintf("- %s", n.Text))
	}
	return "\n" + strings.Join(parts, "\n")
}

func nudgeAlreadySurfaced(state *IddState, text string) bool {
	norm := strings.ToLower(strings.TrimSpace(text))
	for _, n := range state.Nudges {
		if strings.ToLower(strings.TrimSpace(n.Text)) == norm {
			return true
		}
	}
	return false
}

func shortCommit(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	return c
}
