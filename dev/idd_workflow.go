package dev

import (
	"errors"
	"fmt"
	"strings"

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
}

// FinishIddSignal is the payload for SignalNameFinishIdd, asking the workflow
// to merge the idd worktree branch into TargetBranch and exit.
type FinishIddSignal struct {
	TargetBranch string `json:"targetBranch"`
}

// IddSubtask tracks an intent sub-task launched by the IddWorkflow.
type IddSubtask struct {
	FlowId string `json:"flowId"`
	Commit string `json:"commit"`
	Status string `json:"status"`
}

// IddClarification is a question surfaced by a sub-task about ambiguous or
// contradictory intent.
type IddClarification struct {
	SubtaskFlowId string `json:"subtaskFlowId"`
	Question      string `json:"question"`
}

// IddState is the query response describing an IddWorkflow's progress.
type IddState struct {
	// DefaultTargetBranch is the branch the idd worktree was created off of,
	// surfaced so the finish-flow UI can default its merge target.
	DefaultTargetBranch string             `json:"defaultTargetBranch"`
	Subtasks            []IddSubtask       `json:"subtasks"`
	Clarifications      []IddClarification `json:"clarifications"`
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
	state := &IddState{
		Subtasks:       []IddSubtask{},
		Clarifications: []IddClarification{},
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

	startSubtaskCh := workflow.GetSignalChannel(dCtx, SignalNameStartIntentSubtask)
	requestForUserCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameRequestForUser)
	subtaskUnblockedCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameSubtaskUnblocked)
	finishIddCh := workflow.GetSignalChannel(dCtx, SignalNameFinishIdd)
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
			// Spawn a coroutine so committing and running the sub-task to
			// completion doesn't block the selector from handling more signals.
			workflow.Go(dCtx, func(goCtx workflow.Context) {
				runIntentSubtask(dCtx.WithContext(goCtx), input, sig, state)
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
			setSubtaskStatus(state, req.OriginWorkflowId, "blocked")
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
			setSubtaskStatus(state, sig.FlowId, "in_progress")
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

		selector.AddReceive(workflowClosedCh, func(c workflow.ReceiveChannel, _ bool) {
			var closure WorkflowClosure
			c.Receive(dCtx, &closure)
			setSubtaskStatus(state, closure.FlowId, closure.Reason)
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

func setSubtaskStatus(state *IddState, flowId, status string) {
	for i := range state.Subtasks {
		if state.Subtasks[i].FlowId == flowId {
			state.Subtasks[i].Status = status
			return
		}
	}
}

// runIntentSubtask commits the current intent state and launches a BasicDev
// sub-task that implements it, tracking the sub-task's status in state.
func runIntentSubtask(dCtx DevContext, input IddWorkflowInput, sig StartIntentSubtaskSignal, state *IddState) {
	log := workflow.GetLogger(dCtx)

	reqInfo, err := commitIntent(dCtx, input.Title, sig.Update)
	if err != nil {
		log.Error("Failed to commit intent for sub-task", "Error", err)
		return
	}

	branch := dCtx.Worktree.Name
	childOptions := workflow.ChildWorkflowOptions{
		WorkflowID:        "flow_" + ksuidSideEffect(dCtx),
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	}
	if workflow.GetVersion(dCtx, "child-flow-sidekick-version-memo", workflow.DefaultVersion, 1) == 1 {
		childOptions.Memo = map[string]interface{}{
			"sidekickVersion": sidekickVersionSideEffect(dCtx),
		}
	}
	childCtx := workflow.WithChildOptions(dCtx, childOptions)
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
		return
	}
	state.Subtasks = append(state.Subtasks, IddSubtask{
		FlowId: we.ID,
		Commit: reqInfo.Commit,
		Status: "in_progress",
	})

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
	setSubtaskStatus(state, we.ID, status)

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

	return IntentRequirementsInfo{
		Commit: commit,
		Diff:   showOutput.Stdout,
		Update: update,
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
