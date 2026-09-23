package dev

import (
	"fmt"
	"strings"

	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type MarkReadyForReviewArgs struct {
	AllIntentChangesFullySatisfied bool   `json:"allIntentChangesFullySatisfied" jsonschema:"description=Whether every intent change is fully implemented; partial completion must be false."`
	RemainingWork                  string `json:"remainingWork" jsonschema:"description=Describe everything still needed to satisfy the intent, or explicitly explain that nothing remains."`
}

var markReadyForReviewTool = llm.Tool{
	Name:        "mark_ready_for_review",
	Description: "Mark the entire intent implementation ready for human review, not just the latest subtask. Assess all remaining work first. Fails if intent is uncommitted, subtasks are ongoing, or intent is not fully satisfied.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&MarkReadyForReviewArgs{}),
}

func checkIddReviewSubtasks(state *IddState) error {
	if state.Finishing {
		return fmt.Errorf("the flow is already finishing")
	}
	if pending := pendingSubtaskFlowIds(state); len(pending) > 0 {
		return fmt.Errorf("subtasks are still ongoing: %s", strings.Join(pending, ", "))
	}
	if state.InFlightSubtaskRunners > 0 {
		return fmt.Errorf("%d subtask runners are still settling", state.InFlightSubtaskRunners)
	}
	return nil
}

func markIddReadyForReview(dCtx DevContext, state *IddState, args MarkReadyForReviewArgs) error {
	if strings.TrimSpace(args.RemainingWork) == "" {
		return fmt.Errorf("remainingWork must describe what is left, or explain that nothing remains")
	}
	if !args.AllIntentChangesFullySatisfied {
		return fmt.Errorf("intent changes are not fully satisfied: %s", args.RemainingWork)
	}
	if err := checkIddReviewSubtasks(state); err != nil {
		return err
	}
	generation := state.reviewGeneration
	var output env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"status", "--porcelain", "--untracked-files=all", "--", "intent"},
	}).Get(dCtx, &output)
	if err != nil {
		return fmt.Errorf("failed to check uncommitted intent: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("failed to check uncommitted intent (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	if strings.TrimSpace(output.Stdout) != "" {
		return fmt.Errorf("uncommitted intent must be dispatched before review:\n%s", output.Stdout)
	}
	if err := checkIddReviewSubtasks(state); err != nil {
		return err
	}
	if generation != state.reviewGeneration {
		return fmt.Errorf("new work was dispatched during the intent check; reassess remaining intent")
	}
	ma := state.mergeApproval
	if ma == nil || ma.req.FlowActionId == "" {
		return fmt.Errorf("merge approval is not available yet; retry after initialization")
	}
	if state.reviewReady {
		return nil
	}
	if err := flow_action.SignalParentRequestForUser(dCtx.ExecContext, ma.req); err != nil {
		return err
	}
	if generation != state.reviewGeneration {
		var ima *DevAgentManagerActivities
		if err := workflow.ExecuteActivity(dCtx, ima.UpdateTaskByTaskId, ma.input.WorkspaceId, ma.input.TaskId, TaskUpdate{
			Status:    domain.TaskStatusInProgress,
			AgentType: domain.AgentTypeLLM,
		}).Get(dCtx, nil); err != nil {
			return fmt.Errorf("new work invalidated review readiness; failed to restore task status: %w", err)
		}
		return fmt.Errorf("new work was dispatched during review notification; reassess remaining intent")
	}
	if err := checkIddReviewSubtasks(state); err != nil {
		return err
	}
	state.reviewReady = true
	return nil
}
