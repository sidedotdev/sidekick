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

type IddOptions struct {
	EnvType           env.EnvType            `json:"envType,omitempty" default:"local"`
	RepoMode          env.RepoMode           `json:"repoMode,omitempty" default:"worktree"`
	StartBranch       *string                `json:"startBranch,omitempty"`
	ConfigOverrides   common.ConfigOverrides `json:"configOverrides"`
	ContextGatherType ContextGatherType      `json:"contextGatherType,omitempty"`
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
	// Planned runs the sub-task as a PlannedDev child (an explicit multi-step
	// plan is built before coding) rather than the default BasicDev child. The
	// orchestrator sets this for larger sub-tasks spanning disparate changes.
	Planned bool
	// PromptOnly omits the full intent diff from the sub-task's requirements,
	// leaving only ScopePrompt to direct it (the orchestrator's free-form
	// 'prompt' scope). The orchestrator then remains responsible for following
	// up on intent the prompt does not cover.
	PromptOnly bool
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

// IddNudge is a short, non-blocking thought the background orchestrator
// surfaces about the current intent: "have you considered…?", "this looks
// underspecified", etc. Nudges never block work; they're advisory hints the
// human can take or ignore. AnchorText is an optional verbatim snippet from
// the intent the nudge relates to, intended for future hover-highlight UX.
type IddNudge struct {
	Text       string `json:"text"`
	AnchorText string `json:"anchorText,omitempty"`
}

// IddState coordinates the IDD workflow's coroutines.
type IddState struct {
	// DefaultTargetBranch is the branch the idd worktree was created off of,
	// surfaced so the finish-flow UI can default its merge target.
	DefaultTargetBranch string       `json:"defaultTargetBranch"`
	Subtasks            []IddSubtask `json:"subtasks"`
	Nudges              []IddNudge   `json:"nudges"`
	// AutoMode indicates whether the background orchestrator will auto-create
	// sub-tasks when intent edits settle in the worktree.
	AutoMode bool `json:"autoMode"`
	// PendingSubtaskNotices queues human-readable notices about sub-tasks
	// that reached a terminal status since the last orchestrator turn. The
	// next turn that actually runs drains them into its prompt so the
	// orchestrator knows exactly which sub-task just finished (and how)
	// instead of inferring it from the sub-task summary. Workflow-internal
	// only; never surfaced on the canvas.
	PendingSubtaskNotices []string `json:"-"`
	// Finishing is set once the merge approval starts finishing the flow. Work
	// dispatched after that would merge into a branch that is about to be
	// archived, so sub-task dispatch is refused until the finish completes or
	// fails. Workflow-internal only.
	Finishing bool `json:"-"`
	// InFlightSubtaskRunners counts the coroutines dispatching or supervising a
	// sub-task. A sub-task's work reaches the idd branch through its own
	// auto-merge, which can land after it reports closure, so only the runner
	// returning proves that work has settled. Workflow-internal only.
	InFlightSubtaskRunners int `json:"-"`

	mergeApproval    *iddMergeApproval
	reviewReady      bool
	reviewGeneration uint64
	intentTree       string
	intentBaseTree   string
}

// beginSubtaskRunner registers a sub-task runner as in flight. It must be
// called before the dispatch yields (reservation included), so a finish
// starting in that window still waits for the runner.
func (s *IddState) beginSubtaskRunner() {
	s.InFlightSubtaskRunners++
}

// endSubtaskRunner marks a sub-task runner as settled, including runners that
// refused to start their sub-task, so a finish's drain always completes.
func (s *IddState) endSubtaskRunner() {
	s.InFlightSubtaskRunners--
}

// iddParentSetupOptions returns the env type and repo mode used to provision
// the top-level IDD worktree. It is always server-local so intent authoring,
// the committed baseline, git operations, finishing and the edit watcher all
// act on a path that exists on the sidekick host; the environment selected by
// the user applies only to implementation children (see runIntentSubtask).
// Explicit values are returned so repo-config or override defaults can't
// reintroduce a remote parent. Gated by version because histories recorded
// while the parent could be remote provisioned that sandbox here, and replays
// must keep their original activity sequence; such in-flight workflows are
// deliberately not migrated.
func iddParentSetupOptions(ctx workflow.Context, selected IddOptions) (envType string, repoMode string) {
	if workflow.GetVersion(ctx, "idd-local-parent", workflow.DefaultVersion, 1) >= 1 {
		return string(env.EnvTypeLocal), string(env.RepoModeWorktree)
	}
	return string(selected.EnvType), string(selected.RepoMode)
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

	autoModeDefaultVersion := workflow.GetVersion(ctx, "idd-auto-mode-default-on", workflow.DefaultVersion, 1)
	state := &IddState{
		Subtasks: []IddSubtask{},
		Nudges:   []IddNudge{},
		AutoMode: autoModeDefaultVersion >= 1,
	}

	parentEnvType, parentRepoMode := iddParentSetupOptions(ctx, input.IddOptions)

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, parentEnvType, parentRepoMode, input.StartBranch, input.Title, input.ConfigOverrides)
	if err != nil {
		signalWorkflowFailureOrCancel(ctx)
		return err
	}
	dCtx.ContextGatherType = input.ContextGatherType
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
	if err = SetupModelConfigHandlers(dCtx); err != nil {
		return err
	}
	if err = SetupModalConfigHandlers(dCtx); err != nil {
		return err
	}
	SetupProfileChangeHandler(dCtx)

	state.DefaultTargetBranch = dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)

	// Temporal coroutines share state through cooperative scheduling. Avoid
	// yielding midway through mutations so readers see a consistent snapshot.

	// The canvas reads auto mode and nudges from the IDD flow record rather
	// than from workflow state, so seed them as soon as the flow is set up.
	persistIddFlowMetadata(dCtx, input, state)

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
	// Defined before the drainer coroutine below so the trigger can be handed
	// to runIddOrchestratorTurn (and from there to runIntentSubtask), which
	// request a turn whenever a sub-task reaches a terminal status. No-op
	// while orchestratorTriggerCh is nil (pre-orchestrator histories).
	requestOrchestratorTurn := func() {
		if orchestratorTriggerCh == nil {
			return
		}
		// Non-blocking send: if a turn is already queued the new trigger is
		// dropped (it would observe the same or newer state anyway).
		_ = orchestratorTriggerCh.SendAsync(RunIddOrchestratorSignal{})
	}
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
		workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
			for {
				var sig RunIddOrchestratorSignal
				if !orchestratorTriggerCh.Receive(goCtx, &sig) {
					return
				}
				// The orchestrator runs every turn so it can surface nudges
				// about ambiguous/contradictory intent regardless of whether
				// auto sub-task creation is enabled; AutoMode only gates the
				// start_intent_subtask tool inside the turn.
				runIddOrchestratorTurn(dCtx.WithContext(goCtx), input, state, orchestratorChat, !state.AutoMode, requestOrchestratorTurn)
			}
		})
	}

	// Background edit watcher: a long-running activity that returns when the
	// IDD worktree has been quiet for a short idle window after at least one
	// intent-file edit. The workflow re-launches it after each return so the
	// orchestrator gets a steady, server-side trigger that does not depend on
	// the canvas being open. The parent worktree is local for all new IDD
	// flows, so this covers every selected child environment; the env type
	// check only skips legacy histories whose parent was provisioned remotely
	// and whose worktree path isn't on the worker's filesystem.
	startEditWatcher := func() {
		envType := dCtx.EnvContainer.Env.GetType()
		if envType != env.EnvTypeLocal && envType != env.EnvTypeLocalGitWorktree {
			return
		}
		worktreeDir := dCtx.EnvContainer.Env.GetWorkingDirectory()
		if worktreeDir == "" {
			return
		}
		workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
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

	// The merge-approval request is how the user finishes the flow, and how
	// they see what would be merged as work lands, so it is raised up front and
	// kept pending until approved.
	mergeApproval := startIddMergeApproval(dCtx, input, state)

	if err = runIddMainLoop(dCtx, input, state, mergeApproval, requestOrchestratorTurn); err != nil {
		return err
	}

	if closureErr := signalWorkflowClosure(dCtx, "completed"); closureErr != nil {
		workflow.GetLogger(dCtx).Error("Failed to signal idd workflow closure", "Error", closureErr)
	}
	return nil
}

// runIddMainLoop handles the signals an IDD flow lives on — sub-task starts,
// sub-task user requests and closures, auto-mode and orchestrator triggers —
// until the merge approval reports the flow finished or the workflow is
// canceled. It is the only consumer of the sub-task closure channel, so every
// sub-task status transition observed by waiters (e.g. cancelPendingSubtasks)
// flows through here.
func runIddMainLoop(dCtx DevContext, input IddWorkflowInput, state *IddState, mergeApproval *iddMergeApproval, requestOrchestratorTurn func()) error {
	startSubtaskCh := workflow.GetSignalChannel(dCtx, SignalNameStartIntentSubtask)
	requestForUserCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameRequestForUser)
	subtaskUnblockedCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameSubtaskUnblocked)
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
			if state.Finishing {
				workflow.GetLogger(dCtx).Info("Ignoring start intent sub-task signal: the idd flow is finishing")
				return
			}
			state.beginSubtaskRunner()
			// Pre-reserve the sub-task entry synchronously so the canvas (and
			// any subsequent orchestrator turn) sees it immediately, before
			// the commit and child-workflow start yields complete. Version-
			// gated because the original code generated the flow id (via
			// workflow.SideEffect) only after commitIntent had run, so older
			// histories have the SideEffect marker after the commit activity
			// rather than before the receive returns.
			var flowId string
			if workflow.GetVersion(dCtx, "idd-prereserve-subtask", workflow.DefaultVersion, 1) >= 1 {
				flowId = reservePendingSubtask(dCtx, input, state, sig.ScopePrompt)
			}
			// Spawn a coroutine so committing and running the sub-task to
			// completion doesn't block the selector from handling more signals.
			workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
				defer state.endSubtaskRunner()
				runIntentSubtask(dCtx.WithContext(goCtx), input, sig, state, flowId, requestOrchestratorTurn)
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
			updateSubtaskStatus(dCtx, input, state, req.OriginWorkflowId, "blocked")
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
			updateSubtaskStatus(dCtx, input, state, sig.FlowId, "in_progress")
		})

		selector.AddReceive(mergeApproval.finishedCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(dCtx, nil)
			finished = true
		})

		selector.AddReceive(setAutoModeCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig SetIddAutoModeSignal
			c.Receive(dCtx, &sig)
			state.AutoMode = sig.Enabled
			persistIddFlowMetadata(dCtx, input, state)
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
			updateSubtaskStatus(dCtx, input, state, closure.FlowId, closure.Reason)
		})

		selector.Select(dCtx)

		if finished {
			return nil
		}

		if dCtx.Err() != nil {
			return dCtx.Err()
		}
	}
}

// setSubtaskStatus records a sub-task status transition in workflow state,
// returning the updated sub-task when one matched the given flow id.
func setSubtaskStatus(state *IddState, flowId, status string, now time.Time) (IddSubtask, bool) {
	for i := range state.Subtasks {
		if state.Subtasks[i].FlowId == flowId {
			state.Subtasks[i].Status = status
			state.Subtasks[i].UpdatedAt = now
			return state.Subtasks[i], true
		}
	}
	return IddSubtask{}, false
}

func subtaskByFlowId(state *IddState, flowId string) (IddSubtask, bool) {
	for _, subtask := range state.Subtasks {
		if subtask.FlowId == flowId {
			return subtask, true
		}
	}
	return IddSubtask{}, false
}

// updateSubtaskStatus records a sub-task status transition and persists the
// matching flow record.
func updateSubtaskStatus(dCtx DevContext, input IddWorkflowInput, state *IddState, flowId, status string) {
	if subtask, ok := setSubtaskStatus(state, flowId, status, workflow.Now(dCtx)); ok {
		persistSubtaskFlow(dCtx, input, subtask)
	}
}

// persistSubtaskFlow writes the sub-task's flow record so the canvas can list
// sub-tasks and their statuses without querying workflow state, and so the
// sub-task's flow view (including any pending user requests it raises when
// intent is ambiguous) can be opened from the canvas and answered. Records are
// parented to the IDD task rather than the IDD flow, which lets the existing
// completion handler block and then unblock that task.
// FIXME: the IDD flow needs a redesign from scratch rather than further
// incremental patching. Sub-task state is mirrored across in-memory workflow
// state, flow records and flow actions, and those mirror writes are issued
// from several coroutines that each yield mid-write, so statuses can land out
// of order — a finished sub-task can end up recorded as still running. A
// simpler design with a single source of truth and a single owner driving the
// sub-task lifecycle would remove this whole class of races.
func persistSubtaskFlow(dCtx DevContext, input IddWorkflowInput, subtask IddSubtask) {
	var ima *DevAgentManagerActivities
	actCtx := setActivityOptions(dCtx)
	flow := domain.Flow{
		WorkspaceId: input.WorkspaceId,
		Id:          subtask.FlowId,
		Type:        domain.FlowTypeBasicDev,
		ParentId:    input.TaskId,
		Status:      subtask.Status,
		Title:       subtask.Title,
		Created:     subtask.CreatedAt,
		Updated:     subtask.UpdatedAt,
	}
	if err := workflow.ExecuteActivity(actCtx, ima.PutWorkflow, flow).Get(actCtx, nil); err != nil {
		workflow.GetLogger(dCtx).Error("Failed to persist intent sub-task flow record", "Error", err, "FlowId", subtask.FlowId)
	}
}

// Keys under which runtime IDD state the canvas polls is stored on the IDD
// flow record's metadata.
const (
	IddMetadataKeyAutoMode = "autoMode"
	IddMetadataKeyNudges   = "nudges"
)

// persistIddFlowMetadata mirrors the runtime state the canvas needs — auto
// mode and nudges — onto the IDD flow's own record.
func persistIddFlowMetadata(dCtx DevContext, input IddWorkflowInput, state *IddState) {
	log := workflow.GetLogger(dCtx)
	var ima *DevAgentManagerActivities
	actCtx := setActivityOptions(dCtx)
	flowId := workflow.GetInfo(dCtx).WorkflowExecution.ID

	var flow domain.Flow
	if err := workflow.ExecuteActivity(actCtx, ima.GetWorkflow, input.WorkspaceId, flowId).Get(actCtx, &flow); err != nil {
		log.Error("Failed to read idd flow record for metadata update", "Error", err)
		return
	}
	if flow.Metadata == nil {
		flow.Metadata = map[string]any{}
	}
	flow.Metadata[IddMetadataKeyAutoMode] = state.AutoMode
	flow.Metadata[IddMetadataKeyNudges] = state.Nudges
	flow.Updated = workflow.Now(dCtx)
	if err := workflow.ExecuteActivity(actCtx, ima.PutWorkflow, flow).Get(actCtx, nil); err != nil {
		log.Error("Failed to persist idd flow metadata", "Error", err)
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
func reservePendingSubtask(dCtx DevContext, input IddWorkflowInput, state *IddState, scopePrompt string) string {
	flowId := "flow_" + ksuidSideEffect(dCtx)
	now := workflow.Now(dCtx)
	subtask := IddSubtask{
		FlowId:      flowId,
		Status:      "pending",
		ScopePrompt: scopePrompt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	state.Subtasks = append(state.Subtasks, subtask)
	state.reviewGeneration++
	if state.mergeApproval != nil {
		state.reviewReady = false
		var ima *DevAgentManagerActivities
		err := workflow.ExecuteActivity(dCtx, ima.UpdateTaskByTaskId, input.WorkspaceId, input.TaskId, TaskUpdate{
			Status:    domain.TaskStatusInProgress,
			AgentType: domain.AgentTypeLLM,
		}).Get(dCtx, nil)
		if err != nil {
			workflow.GetLogger(dCtx).Error("Failed to mark IDD task in progress", "Error", err)
		}
	}
	persistSubtaskFlow(dCtx, input, subtask)
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
//
// requestOrchestratorTurn, when non-nil, is invoked once the child workflow
// reaches a terminal status so the orchestrator promptly re-evaluates any
// remaining un-dispatched intent.
func runIntentSubtask(dCtx DevContext, input IddWorkflowInput, sig StartIntentSubtaskSignal, state *IddState, flowId string, requestOrchestratorTurn func()) {
	log := workflow.GetLogger(dCtx)
	preReserved := flowId != ""

	reqInfo, err := commitIntent(dCtx, input.Title, sig.Update)
	if err != nil {
		log.Error("Failed to commit intent for sub-task", "Error", err)
		if preReserved {
			updateSubtaskStatus(dCtx, input, state, flowId, "failed")
		}
		return
	}
	if state.mergeApproval != nil {
		state.mergeApproval.requestDiffRefresh()
	}

	reqInfo.ScopePrompt = sig.ScopePrompt
	reqInfo.PromptOnly = sig.PromptOnly

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

	if !preReserved {
		flowId = "flow_" + ksuidSideEffect(dCtx)
	}

	// A redundant second title generation used to run here. It is retained
	// only for sub-tasks whose histories already recorded that extra LLM
	// sequence, since dropping it outright would break their replay.
	if workflow.GetVersion(dCtx, "idd-subtask-single-title-generation", workflow.DefaultVersion, 1) < 1 &&
		workflow.GetVersion(dCtx, "idd-subtask-generated-title", workflow.DefaultVersion, 1) >= 1 {
		if generatedTitle, titleErr := generateIntentSubtaskTitle(dCtx, reqInfo.Commit, reqInfo.Diff); titleErr != nil {
			log.Error("Failed to generate intent sub-task title", "Error", titleErr)
		} else {
			title = generatedTitle
		}
	}

	// Committing and title generation yielded, so a finish may have begun since
	// this runner was dispatched. Starting the child now would produce work on a
	// branch that is about to be archived, and the finish could merge before
	// that work lands.
	if state.Finishing {
		log.Info("Not starting intent sub-task: the idd flow is finishing", "FlowId", flowId)
		if preReserved {
			updateSubtaskStatus(dCtx, input, state, flowId, "canceled")
		}
		return
	}

	branch := dCtx.Worktree.Name
	childOptions := workflow.ChildWorkflowOptions{
		WorkflowID:        flowId,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	}
	if workflow.GetVersion(dCtx, "child-flow-sidekick-version-memo", workflow.DefaultVersion, 1) == 1 {
		childOptions.Memo = map[string]interface{}{
			"sidekickVersion": sidekickVersionSideEffect(dCtx),
		}
	}
	if workflow.GetVersion(dCtx, "flow-workflow-task-timeout", workflow.DefaultVersion, 1) == 1 {
		childOptions.WorkflowTaskTimeout = FlowWorkflowTaskTimeout
	}
	childCtx := workflow.WithChildOptions(dCtx, childOptions)
	requirements := renderIntentRequirements(reqInfo)
	var childFuture workflow.ChildWorkflowFuture
	if sig.Planned {
		childFuture = workflow.ExecuteChildWorkflow(childCtx, PlannedDevWorkflow, PlannedDevInput{
			WorkspaceId:  input.WorkspaceId,
			Requirements: requirements,
			Title:        title,
			RepoDir:      input.RepoDir,
			PlannedDevOptions: PlannedDevOptions{
				DetermineRequirements: false,
				EnvType:               input.EnvType,
				RepoMode:              input.RepoMode,
				StartBranch:           &branch,
				ConfigOverrides:       input.ConfigOverrides,
				ContextGatherType:     input.ContextGatherType,
				AutoMerge:             true,
				Idd:                   true,
			},
		})
	} else {
		childFuture = workflow.ExecuteChildWorkflow(childCtx, BasicDevWorkflow, BasicDevWorkflowInput{
			WorkspaceId:  input.WorkspaceId,
			Requirements: requirements,
			Title:        title,
			RepoDir:      input.RepoDir,
			BasicDevOptions: BasicDevOptions{
				DetermineRequirements: false,
				EnvType:               input.EnvType,
				RepoMode:              input.RepoMode,
				StartBranch:           &branch,
				ConfigOverrides:       input.ConfigOverrides,
				ContextGatherType:     input.ContextGatherType,
				AutoMerge:             true,
				Idd:                   true,
			},
		})
	}

	var we workflow.Execution
	if startErr := childFuture.GetChildWorkflowExecution().Get(childCtx, &we); startErr != nil {
		log.Error("Intent sub-task failed to start", "Error", startErr)
		if preReserved {
			updateSubtaskStatus(dCtx, input, state, flowId, "failed")
		}
		return
	}

	now := workflow.Now(dCtx)
	if preReserved {
		for i := range state.Subtasks {
			if state.Subtasks[i].FlowId == flowId {
				state.Subtasks[i].Title = title
				state.Subtasks[i].Commit = reqInfo.Commit
				state.Subtasks[i].Status = "in_progress"
				state.Subtasks[i].DispatchedDiff = reqInfo.Diff
				state.Subtasks[i].UpdatedAt = now
				break
			}
		}
	} else {
		state.Subtasks = append(state.Subtasks, IddSubtask{
			FlowId:         we.ID,
			Title:          title,
			Commit:         reqInfo.Commit,
			Status:         "in_progress",
			ScopePrompt:    sig.ScopePrompt,
			DispatchedDiff: reqInfo.Diff,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}

	if subtask, ok := subtaskByFlowId(state, we.ID); ok {
		persistSubtaskFlow(dCtx, input, subtask)
	}

	status := "completed"
	// The child result is only decoded for BasicDev sub-tasks, where it's a
	// plain summary string; PlannedDevWorkflow returns a plan-execution struct
	// that isn't worth surfacing to the orchestrator.
	var childResult string
	var childErr error
	if sig.Planned {
		childErr = childFuture.Get(childCtx, nil)
	} else {
		childErr = childFuture.Get(childCtx, &childResult)
	}
	if childErr != nil {
		if temporal.IsCanceledError(childErr) {
			status = "canceled"
		} else {
			status = "failed"
			log.Error("Intent sub-task failed", "Error", childErr)
		}
	}
	updateSubtaskStatus(dCtx, input, state, we.ID, status)
	if state.mergeApproval != nil {
		state.mergeApproval.requestDiffRefresh()
	}

	// Prompt the orchestrator to re-evaluate remaining un-dispatched intent as
	// soon as a sub-task lands (or fails/cancels) instead of waiting for the
	// next intent edit or the edit watcher's MaxWait backstop. The queued
	// notice tells the next turn which sub-task finished and how; because it
	// is queued before the coalescing SendAsync trigger, it is delivered even
	// when this trigger coalesces with an already-queued turn. Version-gated
	// so histories recorded before this change replay without the extra
	// version marker.
	if workflow.GetVersion(dCtx, "idd-subtask-terminal-orchestrator-turn", workflow.DefaultVersion, 1) >= 1 && requestOrchestratorTurn != nil {
		state.PendingSubtaskNotices = append(state.PendingSubtaskNotices,
			subtaskTerminalNotice(title, we.ID, status, childResult, childErr))
		requestOrchestratorTurn()
	}
}

// maxSubtaskNoticeResultLen caps the child-result excerpt embedded in a
// terminal-status notice so a verbose sub-task result can't bloat the
// orchestrator's turn prompt.
const maxSubtaskNoticeResultLen = 2000

// subtaskTerminalNotice renders a human-readable event line describing a
// sub-task that reached a terminal status, for inclusion in the next
// orchestrator turn prompt. childResult, when non-empty, is the sub-task
// child workflow's result summary.
func subtaskTerminalNotice(title, flowId, status, childResult string, childErr error) string {
	switch status {
	case "completed":
		notice := fmt.Sprintf("Sub-task %q (%s) is now complete; its changes were merged into the IDD worktree branch.", title, flowId)
		if result := strings.TrimSpace(childResult); result != "" {
			if len(result) > maxSubtaskNoticeResultLen {
				result = result[:maxSubtaskNoticeResultLen] + "…"
			}
			notice += " Result: " + result
		}
		return notice
	case "canceled":
		return fmt.Sprintf("Sub-task %q (%s) was canceled; its slice of intent was NOT implemented or merged.", title, flowId)
	default:
		notice := fmt.Sprintf("Sub-task %q (%s) failed; its slice of intent was NOT implemented or merged.", title, flowId)
		if childErr != nil {
			notice += " Error: " + childErr.Error()
		}
		return notice
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

	err := flow_action.PerformActivityWithUserRetry(dCtx.ExecContext, "git_commit", git.GitCommitActivity, nil, *dCtx.EnvContainer, git.GitCommitParams{
		CommitMessage:         commitMessage,
		CommitAll:             true,
		IgnoreNothingToCommit: true,
	})
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
// waits for the work they were doing to settle, so nothing lands on the idd
// worktree branch after the caller is done with it.
//
// Reaching a terminal status is not enough to prove that: a canceled sub-task
// reports its closure before running its own cleanup, and its auto-merge into
// the idd branch can complete after that. The runner coroutine supervising the
// child only returns once the child workflow has actually closed, so the drain
// waits for the runners too — including those that refuse to start a sub-task
// while finishing, which settle immediately.
func cancelPendingSubtasks(dCtx DevContext, state *IddState) error {
	for _, flowId := range pendingSubtaskFlowIds(state) {
		// A sub-task reserved but not yet started has no workflow to cancel;
		// its runner sees the flow finishing and closes the reservation out.
		if err := workflow.RequestCancelExternalWorkflow(dCtx, flowId, "").Get(dCtx, nil); err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to request cancel of intent sub-task", "FlowId", flowId, "Error", err)
		}
	}
	if err := workflow.Await(dCtx, func() bool {
		return len(pendingSubtaskFlowIds(state)) == 0 && state.InFlightSubtaskRunners == 0
	}); err != nil {
		return fmt.Errorf("waiting for intent sub-tasks to finish: %w", err)
	}
	return nil
}
