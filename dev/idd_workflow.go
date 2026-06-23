package dev

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// SignalNameStartIntentSubtask asks an IddWorkflow to commit the current intent
// state in its worktree and launch a sub-task that implements it.
const SignalNameStartIntentSubtask = "startIntentSubtask"

// SignalNameFinishIdd asks an IddWorkflow to merge its worktree into the
// requested target branch, cleanup the worktree, and exit cleanly.
const SignalNameFinishIdd = "finishIdd"

// SignalNameSetIddAutoMode toggles the background orchestrator's auto
// task-creation mode. Other orchestrator behaviors (e.g. surfacing
// clarifications) are unaffected by this toggle.
const SignalNameSetIddAutoMode = "setIddAutoMode"

// SignalNameRunIddOrchestrator asks the IddWorkflow's background orchestrator
// to evaluate the current intent state. When AutoMode is on the orchestrator
// may launch sub-tasks for unimplemented intent and/or surface nudges; when
// AutoMode is off it runs in nudge-only mode (no sub-task creation). The
// frontend fires this after its own idle/edit-activity heuristic decides
// intent has settled, keeping the heuristic out of the workflow.
const SignalNameRunIddOrchestrator = "runIddOrchestrator"

// QueryNameIddState returns the current IddState, including ongoing sub-tasks
// and any clarification questions surfaced by those sub-tasks.
const QueryNameIddState = "idd_state"

type IddOptions struct {
	EnvType         env.EnvType            `json:"envType,omitempty" default:"local"`
	RepoMode        env.RepoMode           `json:"repoMode,omitempty" default:"worktree"`
	StartBranch     *string                `json:"startBranch,omitempty"`
	ConfigOverrides common.ConfigOverrides `json:"configOverrides"`
}

type IddWorkflowInput struct {
	WorkspaceId string
	RepoDir     string
	// TaskId is the parent task of the IDD flow. Sub-task flows are parented to
	// it so that surfacing/answering a sub-task's user request blocks and then
	// unblocks the task via the existing task workflow machinery.
	TaskId string
	// Title is required for IDD flows and is used for branch naming and the
	// intent commit message, since IDD tasks carry no free-form description.
	Title string
	IddOptions
}

// StartIntentSubtaskSignal is the payload for SignalNameStartIntentSubtask.
type StartIntentSubtaskSignal struct {
	// Update marks the sub-task as implementing an update to existing intent
	// rather than the initial intent.
	Update bool
	// ScopePrompt, when non-empty, narrows the sub-task to a specific chunk
	// of the pending intent (the orchestrator's "partial" scope). It is
	// prepended to the rendered requirements so the sub-task focuses on that
	// portion of the diff instead of implementing the entire pending intent.
	ScopePrompt string
}

// FinishIddSignal is the payload for SignalNameFinishIdd, asking the workflow
// to merge the idd worktree branch into TargetBranch and exit.
type FinishIddSignal struct {
	TargetBranch string `json:"targetBranch"`
}

// SetIddAutoModeSignal toggles whether the background orchestrator will
// automatically launch sub-tasks when prompted by RunIddOrchestratorSignal.
type SetIddAutoModeSignal struct {
	Enabled bool `json:"enabled"`
}

// RunIddOrchestratorSignal asks the background orchestrator to evaluate
// current intent and, if auto-mode is enabled, start a sub-task for the
// pending intent chunk. The frontend gates this on its edit-idle heuristic.
type RunIddOrchestratorSignal struct{}

// IddSubtask tracks an intent sub-task launched by the IddWorkflow.
type IddSubtask struct {
	FlowId    string    `json:"flowId"`
	Title     string    `json:"title"`
	Commit    string    `json:"commit"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// ScopePrompt, when non-empty, records the orchestrator's narrowed-scope
	// prompt for this sub-task so the canvas can surface what chunk of intent
	// it was assigned. Empty for whole-diff/user-initiated sub-tasks.
	ScopePrompt string `json:"scopePrompt,omitempty"`
	// DispatchedDiff is the intent diff that was committed and handed to this
	// sub-task as its requirements. The orchestrator includes it (truncated)
	// in its turn prompt for still-in-flight sub-tasks so it can tell which
	// slice of intent has already been assigned and avoid re-dispatching the
	// same chunk on subsequent turns. It is intentionally not surfaced on the
	// canvas — that information is already visible via the sub-task's flow
	// view — and is only meaningful for whole-scope sub-tasks where the
	// ScopePrompt is empty.
	DispatchedDiff string `json:"-"`
}

// IddClarification is a question raised by a sub-task that blocks its progress
// until the user answers it. This is the legacy operational channel inherited
// from the task workflow's RequestForUser flow; orchestrator-surfaced
// non-blocking food-for-thought lives in IddState.Nudges instead.
type IddClarification struct {
	SubtaskFlowId string `json:"subtaskFlowId"`
	Question      string `json:"question"`
}

// IddNudge is a short, non-blocking thought the background orchestrator
// surfaces about the current intent: "have you considered…?", "this looks
// underspecified", etc. Nudges never block work; they're advisory hints the
// human can take or ignore. AnchorText is an optional verbatim snippet from
// the intent the nudge relates to, intended for future hover-highlight UX.
type IddNudge struct {
	Text       string `json:"text"`
	AnchorText string `json:"anchorText,omitempty"`
}

// IddState is the query response describing an IddWorkflow's progress.
type IddState struct {
	// DefaultTargetBranch is the branch the idd worktree was created off of,
	// surfaced so the finish-flow UI can default its merge target.
	DefaultTargetBranch string             `json:"defaultTargetBranch"`
	Subtasks            []IddSubtask       `json:"subtasks"`
	Clarifications      []IddClarification `json:"clarifications"`
	Nudges              []IddNudge         `json:"nudges"`
	// AutoMode indicates whether the background orchestrator will auto-create
	// sub-tasks when intent edits settle in the worktree.
	AutoMode bool `json:"autoMode"`
}

// IddWorkflow drives the Intent Driven Development canvas: it sets up a worktree
// for editing intent files, then stays alive listening for signals to commit
// the current intent state and spawn sub-tasks that implement it. Each sub-task
// runs as a BasicDev child off the idd worktree HEAD and auto-merges back into
// the idd worktree branch on completion.
func IddWorkflow(ctx workflow.Context, input IddWorkflowInput) (err error) {
	// don't recover panics in development so we can debug via temporal UI, at
	// the cost of failed tasks appearing stuck without UI feedback in sidekick
	if SideAppEnv != "development" {
		defer func() {
			if r := recover(); r != nil {
				signalWorkflowFailureOrCancel(ctx)
				var ok bool
				err, ok = r.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", r)
				}
			}
		}()
	}

	ctx = utils.DefaultRetryCtx(ctx)

	// Register the idd_state query handler eagerly so the canvas UI can poll
	// state even before SetupDevContext finishes (or if it fails). The handler
	// closes over a pointer so later mutations are visible.
	autoModeDefaultVersion := workflow.GetVersion(ctx, "idd-auto-mode-default-on", workflow.DefaultVersion, 1)
	state := &IddState{
		Subtasks:       []IddSubtask{},
		Clarifications: []IddClarification{},
		Nudges:         []IddNudge{},
		AutoMode:       autoModeDefaultVersion >= 1,
	}
	_ = workflow.SetQueryHandler(ctx, QueryNameIddState, func() (IddState, error) {
		return *state, nil
	})

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, string(input.EnvType), string(input.RepoMode), input.StartBranch, input.Title, input.ConfigOverrides)
	if err != nil {
		signalWorkflowFailureOrCancel(ctx)
		return err
	}
	dCtx.Idd = true
	defer handleFlowCancel(dCtx)
	defer stopActiveDevRun(dCtx)
	defer func() {
		if err != nil && !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
			_ = signalWorkflowClosure(dCtx, "failed")
		}
	}()

	SetupPauseHandler(dCtx, "Paused for user input", nil)
	SetupUserActionHandler(dCtx)
	SetupDevRunConfigQuery(dCtx)
	SetupDevRunStateQuery(dCtx)

	state.DefaultTargetBranch = dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)

	// state is shared across the main selector loop, the orchestrator
	// drainer coroutine, sub-task runner coroutines, and the query handler.
	// This is safe because Temporal Go SDK coroutines are cooperatively
	// scheduled — they only yield at workflow.* calls (Receive, Get, Sleep,
	// etc.) — and query handlers run between workflow tasks, not concurrent
	// with workflow code. A real mutex would break determinism; do not add
	// one. Mutations to state must be single-statement (no yields mid-update)
	// so concurrent readers always see a consistent snapshot.
	_ = workflow.SetQueryHandler(dCtx, QueryNameIddState, func() (IddState, error) {
		return *state, nil
	})

	// Background orchestrator setup (persisted chat history, coalescing
	// trigger channel, drainer coroutine) is version-gated because older
	// IDD workflow histories were recorded before any of these existed.
	// Although NewBufferedChannel/workflow.Go emit no history events, the
	// internal version marker recorded by NewVersionedChatHistory would
	// shift the marker order observed on replay; keeping the whole
	// orchestrator wiring behind one gate makes the feature atomically
	// off for pre-existing workflows.
	orchestratorVersion := workflow.GetVersion(dCtx, "idd-background-orchestrator", workflow.DefaultVersion, 1)
	var orchestratorTriggerCh workflow.Channel
	if orchestratorVersion >= 1 {
		// orchestratorChat persists across orchestrator turns so the background
		// agent can remember which intent chunks it has already dispatched
		// (those tool calls are tagged with ContextTypeIntentTaskStart and
		// retained by ManageChatHistory across trimming).
		orchestratorChat := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)

		// Capacity 1 so concurrent triggers (e.g. a fired idle signal
		// followed quickly by a watcher-activity return) coalesce into at
		// most one pending turn, and a dedicated coroutine drains it
		// serially. This prevents racy parallel turns from both reading
		// state, both deciding to dispatch, and double-creating sub-tasks
		// for the same intent diff.
		orchestratorTriggerCh = workflow.NewBufferedChannel(dCtx, 1)
		workflow.Go(dCtx, func(goCtx workflow.Context) {
			for {
				var sig RunIddOrchestratorSignal
				if !orchestratorTriggerCh.Receive(goCtx, &sig) {
					return
				}
				// The orchestrator runs every turn so it can surface nudges
				// about ambiguous/contradictory intent regardless of whether
				// auto sub-task creation is enabled; AutoMode only gates the
				// start_intent_subtask tool inside the turn.
				runIddOrchestratorTurn(dCtx.WithContext(goCtx), input, state, orchestratorChat, !state.AutoMode)
			}
		})
	}

	requestOrchestratorTurn := func() {
		if orchestratorTriggerCh == nil {
			return
		}
		// Non-blocking send: if a turn is already queued the new trigger is
		// dropped (it would observe the same or newer state anyway).
		_ = orchestratorTriggerCh.SendAsync(RunIddOrchestratorSignal{})
	}

	// Background edit watcher: a long-running activity that returns when the
	// IDD worktree has been quiet for a short idle window after at least one
	// intent-file edit. The workflow re-launches it after each return so the
	// orchestrator gets a steady, server-side trigger that does not depend on
	// the canvas being open. Only enabled for local env types because remote
	// containers do not expose the worktree path to the worker's filesystem.
	startEditWatcher := func() {
		envType := dCtx.EnvContainer.Env.GetType()
		if envType != env.EnvTypeLocal && envType != env.EnvTypeLocalGitWorktree {
			return
		}
		worktreeDir := dCtx.EnvContainer.Env.GetWorkingDirectory()
		if worktreeDir == "" {
			return
		}
		workflow.Go(dCtx, func(goCtx workflow.Context) {
			watchCtx := workflow.WithActivityOptions(goCtx, workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Minute,
				HeartbeatTimeout:    2 * time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					MaximumAttempts: 1,
				},
				WaitForCancellation: true,
			})
			for {
				if goCtx.Err() != nil {
					return
				}
				var out IddWatchEditIdleResult
				err := workflow.ExecuteActivity(watchCtx, IddWatchEditIdleActivity, IddWatchEditIdleInput{
					WorktreeDir:  worktreeDir,
					WatchSubdir:  "intent",
					IdleDuration: 8 * time.Second,
					MaxWait:      25 * time.Minute,
				}).Get(watchCtx, &out)
				if err != nil {
					if goCtx.Err() != nil {
						return
					}
					workflow.GetLogger(goCtx).Warn("IDD edit watcher returned with error; restarting after backoff", "Error", err)
					_ = workflow.Sleep(goCtx, 5*time.Second)
					continue
				}
				// Trigger an orchestrator turn whenever the watcher observed
				// edits, OR when it timed out: MaxWait timing out guarantees
				// the orchestrator gets a chance to act even if the idle
				// heuristic never fired (e.g. continuous trickling edits or
				// edits made during the brief gap between activity
				// invocations). Empty turns are cheap — runIddOrchestratorTurn
				// no-ops on an empty pending diff.
				if len(out.ChangedPaths) > 0 || out.TimedOut {
					requestOrchestratorTurn()
				}
			}
		})
	}
	// Old IDD workflow histories were recorded before the edit watcher
	// existed; scheduling the watcher activity unconditionally would make
	// them non-deterministic on replay. Gate behind a version so only
	// workflows started at or after this change schedule the watcher.
	editWatcherVersion := workflow.GetVersion(dCtx, "idd-edit-watcher", workflow.DefaultVersion, 1)
	if editWatcherVersion >= 1 {
		startEditWatcher()
	}

	startSubtaskCh := workflow.GetSignalChannel(dCtx, SignalNameStartIntentSubtask)
	requestForUserCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameRequestForUser)
	subtaskUnblockedCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameSubtaskUnblocked)
	finishIddCh := workflow.GetSignalChannel(dCtx, SignalNameFinishIdd)
	setAutoModeCh := workflow.GetSignalChannel(dCtx, SignalNameSetIddAutoMode)
	runOrchestratorCh := workflow.GetSignalChannel(dCtx, SignalNameRunIddOrchestrator)
	workflowClosedCh := workflow.GetSignalChannel(dCtx, SignalNameWorkflowClosed)
	finished := false

	// The canvas keeps this workflow alive so the user can launch many sub-tasks
	// over the lifetime of one intent worktree; it ends only on cancellation.
	// TODO support continue-as-new without losing in-memory sub-task state.
	for {
		selector := workflow.NewNamedSelector(dCtx, "iddSelector")

		selector.AddReceive(startSubtaskCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig StartIntentSubtaskSignal
			c.Receive(dCtx, &sig)
			// Pre-reserve the sub-task entry synchronously so the canvas (and
			// any subsequent orchestrator turn) sees it immediately, before
			// the commit and child-workflow start yields complete. Version-
			// gated because the original code generated the flow id (via
			// workflow.SideEffect) only after commitIntent had run, so older
			// histories have the SideEffect marker after the commit activity
			// rather than before the receive returns.
			var flowId string
			if workflow.GetVersion(dCtx, "idd-prereserve-subtask", workflow.DefaultVersion, 1) >= 1 {
				flowId = reservePendingSubtask(dCtx, state, sig.ScopePrompt)
			}
			// Spawn a coroutine so committing and running the sub-task to
			// completion doesn't block the selector from handling more signals.
			workflow.Go(dCtx, func(goCtx workflow.Context) {
				runIntentSubtask(dCtx.WithContext(goCtx), input, sig, state, flowId)
			})
		})

		selector.AddReceive(requestForUserCh, func(c workflow.ReceiveChannel, _ bool) {
			var req flow_action.RequestForUser
			c.Receive(dCtx, &req)
			// A sub-task blocks on user input when intent is too ambiguous or
			// contradictory to proceed. The top-level task workflow normally
			// translates such a request into a blocked task status, but for
			// IDD sub-tasks no separate task workflow sits between the
			// sub-task and the IDD canvas, so we mark the sub-task blocked
			// here as well. The matching unblock arrives via
			// SignalNameSubtaskUnblocked when the user answers. The request is
			// also forwarded to the parent task workflow so the IDD task
			// itself surfaces a pending user request and is marked blocked.
			// TODO have the orchestrator attempt to resolve clarifications from
			// intent itself before falling back to asking the user, and surface
			// unresolved ones on the canvas.
			setSubtaskStatus(state, req.OriginWorkflowId, "blocked", workflow.Now(dCtx))
			parent := workflow.GetInfo(dCtx).ParentWorkflowExecution
			if parent == nil {
				workflow.GetLogger(dCtx).Error("Cannot forward intent sub-task user request: no parent workflow")
				return
			}
			if sigErr := workflow.SignalExternalWorkflow(dCtx, parent.ID, "", flow_action.SignalNameRequestForUser, req).Get(dCtx, nil); sigErr != nil {
				workflow.GetLogger(dCtx).Error("Failed to forward intent sub-task user request to task workflow", "Error", sigErr)
			}
		})

		selector.AddReceive(subtaskUnblockedCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig flow_action.SubtaskUnblocked
			c.Receive(dCtx, &sig)
			setSubtaskStatus(state, sig.FlowId, "in_progress", workflow.Now(dCtx))
		})

		selector.AddReceive(finishIddCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig FinishIddSignal
			c.Receive(dCtx, &sig)
			if err := finishIdd(dCtx, input, sig, state); err != nil {
				workflow.GetLogger(dCtx).Error("Failed to finish idd flow", "Error", err)
				return
			}
			finished = true
		})

		selector.AddReceive(setAutoModeCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig SetIddAutoModeSignal
			c.Receive(dCtx, &sig)
			state.AutoMode = sig.Enabled
		})

		selector.AddReceive(runOrchestratorCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig RunIddOrchestratorSignal
			c.Receive(dCtx, &sig)
			// Queue a turn unconditionally. The drainer runs the orchestrator
			// in nudge-only mode when AutoMode is off so nudges about
			// ambiguous/contradictory intent keep surfacing even when the
			// user has disabled auto sub-task creation.
			requestOrchestratorTurn()
		})

		selector.AddReceive(workflowClosedCh, func(c workflow.ReceiveChannel, _ bool) {
			var closure WorkflowClosure
			c.Receive(dCtx, &closure)
			setSubtaskStatus(state, closure.FlowId, closure.Reason, workflow.Now(dCtx))
		})

		selector.Select(dCtx)

		if finished {
			if closureErr := signalWorkflowClosure(dCtx, "completed"); closureErr != nil {
				workflow.GetLogger(dCtx).Error("Failed to signal idd workflow closure", "Error", closureErr)
			}
			return nil
		}

		if dCtx.Err() != nil {
			return dCtx.Err()
		}
	}
}

func setSubtaskStatus(state *IddState, flowId, status string, now time.Time) {
	for i := range state.Subtasks {
		if state.Subtasks[i].FlowId == flowId {
			state.Subtasks[i].Status = status
			state.Subtasks[i].UpdatedAt = now
			return
		}
	}
}

// reservePendingSubtask synchronously reserves a flow id for an about-to-launch
// sub-task and records it in state with status "pending". This must be called
// from the workflow's selector coroutine (not from an inner workflow.Go) so
// that subsequent orchestrator turns, query handlers, and the "update vs
// initial" classification all observe the in-flight sub-task immediately —
// before runIntentSubtask yields on commitIntent and child-workflow start.
// Without this synchronous reservation the orchestrator could re-decide to
// dispatch for the same pending intent diff, and len(state.Subtasks) would
// mis-classify multiple concurrent first-time starts as "initial".
func reservePendingSubtask(dCtx DevContext, state *IddState, scopePrompt string) string {
	flowId := "flow_" + ksuidSideEffect(dCtx)
	state.Subtasks = append(state.Subtasks, IddSubtask{
		FlowId:      flowId,
		Status:      "pending",
		ScopePrompt: scopePrompt,
	})
	return flowId
}

// runIntentSubtask commits the current intent state and launches a BasicDev
// sub-task that implements it, tracking the sub-task's status in state. When
// flowId is non-empty the caller has already recorded a pending IddSubtask
// entry via reservePendingSubtask, and this function updates that entry in
// place. When flowId is empty the function falls back to the original behavior
// (generate the id after commitIntent, append the entry after the child
// workflow starts) for replay compatibility with histories recorded before
// the pre-reservation version.
func runIntentSubtask(dCtx DevContext, input IddWorkflowInput, sig StartIntentSubtaskSignal, state *IddState, flowId string) {
	log := workflow.GetLogger(dCtx)
	preReserved := flowId != ""

	reqInfo, err := commitIntent(dCtx, input.Title, sig.Update)
	if err != nil {
		log.Error("Failed to commit intent for sub-task", "Error", err)
		if preReserved {
			setSubtaskStatus(state, flowId, "failed", workflow.Now(dCtx))
		}
		return
	}
	reqInfo.ScopePrompt = sig.ScopePrompt

<<<<<<< HEAD
	if !preReserved {
		flowId = "flow_" + ksuidSideEffect(dCtx)
	}
=======
	// The sub-task gets its own descriptive title generated from the committed
	// intent sha & diff, falling back to the IDD task title if generation fails.
	// Gated by version so in-flight sub-tasks recorded before this change replay
	// without the extra LLM activity.
	title := input.Title
	if workflow.GetVersion(dCtx, "idd-subtask-generated-title", workflow.DefaultVersion, 1) >= 1 {
		if generatedTitle, titleErr := generateIntentSubtaskTitle(dCtx, reqInfo.Commit, reqInfo.Diff); titleErr != nil {
			log.Error("Failed to generate intent sub-task title", "Error", titleErr)
		} else {
			title = generatedTitle
		}
	}

>>>>>>> side/improve-intent-subtask-workflow
	branch := dCtx.Worktree.Name
	childCtx := workflow.WithChildOptions(dCtx, workflow.ChildWorkflowOptions{
		WorkflowID:        flowId,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	childFuture := workflow.ExecuteChildWorkflow(childCtx, BasicDevWorkflow, BasicDevWorkflowInput{
		WorkspaceId:  input.WorkspaceId,
		Requirements: renderIntentRequirements(reqInfo),
		RepoDir:      input.RepoDir,
		BasicDevOptions: BasicDevOptions{
			DetermineRequirements: false,
			EnvType:               input.EnvType,
			RepoMode:              input.RepoMode,
			StartBranch:           &branch,
			ConfigOverrides:       input.ConfigOverrides,
			AutoMerge:             true,
			Idd:                   true,
		},
	})

	var we workflow.Execution
	if startErr := childFuture.GetChildWorkflowExecution().Get(childCtx, &we); startErr != nil {
		log.Error("Intent sub-task failed to start", "Error", startErr)
		if preReserved {
			setSubtaskStatus(state, flowId, "failed", workflow.Now(dCtx))
		}
		return
	}

	now := workflow.Now(dCtx)
<<<<<<< HEAD
	if preReserved {
		for i := range state.Subtasks {
			if state.Subtasks[i].FlowId == flowId {
				state.Subtasks[i].Commit = reqInfo.Commit
				state.Subtasks[i].Status = "in_progress"
				state.Subtasks[i].DispatchedDiff = reqInfo.Diff
				state.Subtasks[i].CreatedAt = now
				state.Subtasks[i].UpdatedAt = now
				break
			}
		}
	} else {
		state.Subtasks = append(state.Subtasks, IddSubtask{
			FlowId:         we.ID,
			Commit:         reqInfo.Commit,
			Status:         "in_progress",
			ScopePrompt:    sig.ScopePrompt,
			DispatchedDiff: reqInfo.Diff,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
=======
	state.Subtasks = append(state.Subtasks, IddSubtask{
		FlowId:    we.ID,
		Title:     title,
		Commit:    reqInfo.Commit,
		Status:    "in_progress",
		CreatedAt: now,
		UpdatedAt: now,
	})
>>>>>>> side/improve-intent-subtask-workflow

	// Persist a flow record for the sub-task, parented to the IDD task, so its
	// flow view (and any pending user requests it raises when intent is
	// ambiguous) can be opened from the canvas and answered. Parenting to the
	// task lets the existing completion handler block and then unblock that task.
	var ima *DevAgentManagerActivities
	actCtx := setActivityOptions(dCtx)
	subtaskFlow := domain.Flow{
		WorkspaceId: input.WorkspaceId,
		Id:          we.ID,
		Type:        domain.FlowTypeBasicDev,
		Status:      "in_progress",
		ParentId:    input.TaskId,
	}
	if putErr := workflow.ExecuteActivity(actCtx, ima.PutWorkflow, subtaskFlow).Get(actCtx, nil); putErr != nil {
		log.Error("Failed to persist intent sub-task flow record", "Error", putErr)
	}

	status := "completed"
	if childErr := childFuture.Get(childCtx, nil); childErr != nil {
		if temporal.IsCanceledError(childErr) {
			status = "canceled"
		} else {
			status = "failed"
			log.Error("Intent sub-task failed", "Error", childErr)
		}
	}
	setSubtaskStatus(state, we.ID, status, workflow.Now(dCtx))

	subtaskFlow.Status = status
	if putErr := workflow.ExecuteActivity(actCtx, ima.PutWorkflow, subtaskFlow).Get(actCtx, nil); putErr != nil {
		log.Error("Failed to update intent sub-task flow record", "Error", putErr)
	}
}

// commitIntent commits all pending intent changes in the worktree and returns
// the resulting commit hash and its diff for building sub-task requirements. A
// clean tree is not an error: the sub-task then re-implements the current intent
// state at the existing HEAD.
func commitIntent(dCtx DevContext, title string, update bool) (IntentRequirementsInfo, error) {
	commitMessage := fmt.Sprintf("Intent: %s", title)
	if update {
		commitMessage = fmt.Sprintf("Intent update: %s", title)
	}

	// `git commit -a` alone skips brand-new (untracked) intent files, which
	// would leave HEAD pointing at the previous intent commit and cause the
	// sub-task to be built from stale state. Explicitly staging the worktree
	// first guarantees any new intent file lands in the commit we then read
	// back via `rev-parse HEAD`. Gate behind a version so in-flight workflows
	// recorded before this fix replay against the original command sequence.
	stageIntentVersion := workflow.GetVersion(dCtx, "idd-commit-intent-stage-untracked", workflow.DefaultVersion, 1)
	if stageIntentVersion >= 1 {
		err := workflow.ExecuteActivity(dCtx, git.GitAddActivity, git.GitAddActivityInput{
			EnvContainer: *dCtx.EnvContainer,
			Path:         ".",
		}).Get(dCtx, nil)
		if err != nil {
			return IntentRequirementsInfo{}, fmt.Errorf("failed to stage intent changes: %w", err)
		}
	}

	err := workflow.ExecuteActivity(dCtx, git.GitCommitActivity, *dCtx.EnvContainer, git.GitCommitParams{
		CommitMessage:         commitMessage,
		CommitAll:             true,
		IgnoreNothingToCommit: true,
	}).Get(dCtx, nil)
	if err != nil {
		return IntentRequirementsInfo{}, fmt.Errorf("failed to commit intent: %w", err)
	}

	var headOutput env.EnvRunCommandActivityOutput
	err = workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"rev-parse", "HEAD"},
	}).Get(dCtx, &headOutput)
	if err != nil {
		return IntentRequirementsInfo{}, fmt.Errorf("failed to resolve intent commit hash: %w", err)
	}
	commit := strings.TrimSpace(headOutput.Stdout)

	var showOutput env.EnvRunCommandActivityOutput
	err = workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"show", commit},
	}).Get(dCtx, &showOutput)
	if err != nil {
		return IntentRequirementsInfo{}, fmt.Errorf("failed to get intent diff: %w", err)
	}

	// A "clean" diff ignores whitespace and renders word-level changes so
	// cosmetic reflow of markdown intent doesn't read as a real edit in the
	// requirements prompt. Gate behind a version so older executions replay
	// against the original activity sequence.
	cleanDiff := showOutput.Stdout
	cleanDiffVersion := workflow.GetVersion(dCtx, "idd-commit-intent-clean-diff", workflow.DefaultVersion, 1)
	if cleanDiffVersion >= 1 {
		var cleanOutput env.EnvRunCommandActivityOutput
		err = workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       *dCtx.EnvContainer,
			RelativeWorkingDir: "./",
			Command:            "git",
			Args:               []string{"show", "--word-diff=plain", "--ignore-all-space", commit},
		}).Get(dCtx, &cleanOutput)
		if err != nil {
			return IntentRequirementsInfo{}, fmt.Errorf("failed to get clean intent diff: %w", err)
		}
		cleanDiff = cleanOutput.Stdout
	}

	return IntentRequirementsInfo{
		Commit:    commit,
		Diff:      showOutput.Stdout,
		CleanDiff: cleanDiff,
		Update:    update,
	}, nil
}

// isPendingSubtaskStatus reports whether a sub-task in the given status is
// still in flight (i.e. has not yet reached a terminal status reported back to
// the IddWorkflow via the runIntentSubtask coroutine or a workflow closure).
func isPendingSubtaskStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return false
	default:
		return true
	}
}

// pendingSubtaskFlowIds returns the flow ids of sub-tasks that are still in
// flight according to the canvas's in-memory state.
func pendingSubtaskFlowIds(state *IddState) []string {
	var pending []string
	for _, st := range state.Subtasks {
		if isPendingSubtaskStatus(st.Status) {
			pending = append(pending, st.FlowId)
		}
	}
	return pending
}

// cancelPendingSubtasks requests cancellation of every in-flight sub-task and
// waits for each to reach a terminal status. This stops sub-tasks from racing
// the finish-merge against the idd worktree branch, and lets their own
// cleanup/auto-merge logic settle so the worktree is in a consistent state
// before we merge it elsewhere.
func cancelPendingSubtasks(dCtx DevContext, state *IddState) error {
	pending := pendingSubtaskFlowIds(state)
	for _, flowId := range pending {
		if err := workflow.RequestCancelExternalWorkflow(dCtx, flowId, "").Get(dCtx, nil); err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to request cancel of intent sub-task", "FlowId", flowId, "Error", err)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if err := workflow.Await(dCtx, func() bool {
		return len(pendingSubtaskFlowIds(state)) == 0
	}); err != nil {
		return fmt.Errorf("waiting for intent sub-tasks to finish: %w", err)
	}
	return nil
}

// finishIdd commits any pending intent in the worktree, merges the idd
// worktree branch into the requested target branch, and cleans up the
// worktree. A clean exit lets the parent task workflow mark the IDD task
// completed via the closure signal sent by the caller.
func finishIdd(dCtx DevContext, input IddWorkflowInput, sig FinishIddSignal, state *IddState) error {
	target := strings.TrimSpace(sig.TargetBranch)
	if target == "" {
		target = state.DefaultTargetBranch
	}
	if target == "" {
		return fmt.Errorf("finish idd: no target branch specified")
	}
	if dCtx.Worktree == nil {
		return fmt.Errorf("finish idd: no worktree associated with idd workflow")
	}
	if target == dCtx.Worktree.Name {
		return fmt.Errorf("finish idd: target branch %q is the idd worktree branch", target)
	}

	if err := cancelPendingSubtasks(dCtx, state); err != nil {
		return err
	}

	if _, err := commitIntent(dCtx, input.Title, true); err != nil {
		return fmt.Errorf("failed to commit pending intent before merge: %w", err)
	}

	var mergeResult git.MergeActivityResult
	err := workflow.ExecuteActivity(dCtx, git.GitMergeActivity, *dCtx.EnvContainer, git.GitMergeParams{
		SourceBranch:  dCtx.Worktree.Name,
		TargetBranch:  target,
		CommitMessage: fmt.Sprintf("Finish IDD: %s", input.Title),
		MergeStrategy: git.MergeStrategyMerge,
	}).Get(dCtx, &mergeResult)
	if err != nil {
		return fmt.Errorf("failed to merge idd worktree into %s: %w", target, err)
	}
	if mergeResult.HasConflicts {
		return fmt.Errorf("merge conflicts encountered while finishing idd into %s; resolve them manually", target)
	}

	cleanupErr := workflow.ExecuteActivity(dCtx, git.CleanupWorktreeActivity, *dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "IDD flow finished").Get(dCtx, nil)
	if cleanupErr != nil {
		workflow.GetLogger(dCtx).Warn("Failed to cleanup IDD worktree after finish merge", "Error", cleanupErr)
	}

	return nil
}
