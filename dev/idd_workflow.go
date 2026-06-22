package dev

import (
	"errors"
	"fmt"
	"strings"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
)

// SignalNameStartIntentSubtask asks an IddWorkflow to commit the current intent
// state in its worktree and launch a sub-task that implements it.
const SignalNameStartIntentSubtask = "startIntentSubtask"

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
	Subtasks       []IddSubtask       `json:"subtasks"`
	Clarifications []IddClarification `json:"clarifications"`
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

	state := &IddState{
		Subtasks:       []IddSubtask{},
		Clarifications: []IddClarification{},
	}
	_ = workflow.SetQueryHandler(dCtx, QueryNameIddState, func() (IddState, error) {
		return *state, nil
	})

	startSubtaskCh := workflow.GetSignalChannel(dCtx, SignalNameStartIntentSubtask)
	requestForUserCh := workflow.GetSignalChannel(dCtx, flow_action.SignalNameRequestForUser)
	workflowClosedCh := workflow.GetSignalChannel(dCtx, SignalNameWorkflowClosed)

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
			// Sub-tasks request user input when intent is ambiguous or
			// contradictory; surface those as clarification questions.
			state.Clarifications = append(state.Clarifications, IddClarification{
				SubtaskFlowId: req.OriginWorkflowId,
				Question:      req.Content,
			})
		})

		selector.AddReceive(workflowClosedCh, func(c workflow.ReceiveChannel, _ bool) {
			var closure WorkflowClosure
			c.Receive(dCtx, &closure)
			setSubtaskStatus(state, closure.FlowId, closure.Reason)
		})

		selector.Select(dCtx)

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
	childCtx := workflow.WithChildOptions(dCtx, workflow.ChildWorkflowOptions{
		WorkflowID:        "flow_" + ksuidSideEffect(dCtx),
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
			MergeIntoBranch:       &branch,
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

	status := "completed"
	if childErr := childFuture.Get(childCtx, nil); childErr != nil {
		status = "failed"
		log.Error("Intent sub-task failed", "Error", childErr)
	}
	setSubtaskStatus(state, we.ID, status)
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
