package dev

import (
	"fmt"
	"time"

	"sidekick/env"
	"sidekick/flow_action"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DockerFailureHandler struct {
	Fallback flow_action.ActivityFailureHandler
}

func (h DockerFailureHandler) HandleActivityFailure(eCtx flow_action.ExecContext, actionName string, activityErr error) (flow_action.RetryDecision, error) {
	details, ok := env.AsDockerEngineFailure(activityErr)
	if !ok || workflow.GetVersion(eCtx, "docker-recovery-confirmation", workflow.DefaultVersion, 1) < 1 {
		if h.Fallback != nil {
			return h.Fallback.HandleActivityFailure(eCtx, actionName, activityErr)
		}
		return flow_action.RetryDecisionFail, nil
	}
	if err := eCtx.Err(); err != nil {
		return flow_action.RetryDecisionFail, err
	}
	if eCtx.DisableHumanInTheLoop || details.Host == "" {
		return flow_action.RetryDecisionFail, nil
	}
	if eCtx.GlobalState != nil && eCtx.GlobalState.GetPendingUserAction() != nil {
		return flow_action.RetryDecisionFail, flow_action.PendingActionError
	}

	prompt := fmt.Sprintf("%s failed because Docker on **%s** is unavailable.\n\n"+
		"Restarting Docker interrupts **all containers and workspaces on that engine**, including other flows. "+
		"You can instead check Docker yourself and retry without restarting.\n\n```\n%s\n```",
		actionName, details.Host, activityErr)
	for {
		response, err := flow_action.GetUserApproval(eCtx, "restart_docker", prompt, map[string]any{
			"approveTag":           "restart_docker",
			"rejectTag":            "retry_without_restart",
			"rejectReasonRequired": false,
		})
		if err != nil {
			return flow_action.RetryDecisionFail, err
		}
		if response == nil || response.Approved == nil {
			return flow_action.RetryDecisionFail, fmt.Errorf("Docker recovery requires a restart or retry selection")
		}
		if !*response.Approved {
			return flow_action.RetryDecisionRetry, nil
		}

		options := workflow.GetActivityOptions(eCtx)
		restartCtx := workflow.WithActivityOptions(eCtx, workflow.ActivityOptions{
			TaskQueue:              options.TaskQueue,
			StartToCloseTimeout:    5 * time.Minute,
			ScheduleToStartTimeout: 30 * time.Second,
			RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		err = workflow.ExecuteActivity(restartCtx, env.RestartDockerEngineActivity, env.RestartDockerEngineInput{
			Host: details.Host, Confirmed: true,
		}).Get(restartCtx, nil)
		if err == nil {
			return flow_action.RetryDecisionRetry, nil
		}
		if temporal.IsCanceledError(err) || eCtx.Err() != nil {
			return flow_action.RetryDecisionFail, err
		}
		prompt = fmt.Sprintf("Docker restart on **%s** failed:\n\n```\n%s\n```\n\n"+
			"Retry without restarting, or explicitly confirm another attempt. "+
			"A restart interrupts **all containers and workspaces on that engine**.", details.Host, err)
	}
}
