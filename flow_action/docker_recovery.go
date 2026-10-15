package flow_action

import (
	"fmt"
	"time"

	"sidekick/env"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// HandleDockerFailure reports whether this error belongs to the Docker
// confirmation flow. A handled error with a nil result permits a retry.
func HandleDockerFailure(eCtx ExecContext, actionName string, activityErr error) (bool, error) {
	details, ok := env.AsDockerEngineFailure(activityErr)
	if !ok {
		return false, nil
	}
	if workflow.GetVersion(eCtx, "docker-recovery-confirmation", workflow.DefaultVersion, 1) < 1 {
		return false, nil
	}
	if eCtx.DisableHumanInTheLoop {
		return true, activityErr
	}
	if eCtx.GlobalState != nil && eCtx.GlobalState.GetPendingUserAction() != nil {
		return true, PendingActionError
	}

	prompt := fmt.Sprintf("%s failed because Docker on **%s** is unavailable.\n\n"+
		"Restarting Docker interrupts **all containers and workspaces on that engine**, including other flows. "+
		"You can instead check Docker yourself and retry without restarting.\n\n```\n%s\n```",
		actionName, details.Host, activityErr)
	for {
		response, err := GetUserApproval(eCtx, "restart_docker", prompt, map[string]any{
			"approveTag":           "restart_docker",
			"rejectTag":            "retry_without_restart",
			"rejectReasonRequired": false,
		})
		if err != nil {
			return true, err
		}
		if response == nil || response.Approved == nil {
			return true, fmt.Errorf("Docker recovery requires a restart or retry selection")
		}
		if !*response.Approved {
			return true, nil
		}

		restartCtx := workflow.WithActivityOptions(eCtx, workflow.ActivityOptions{
			TaskQueue:              env.DockerRestartTaskQueue(details.Host),
			StartToCloseTimeout:    5 * time.Minute,
			ScheduleToStartTimeout: 30 * time.Second,
			RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		err = workflow.ExecuteActivity(restartCtx, env.RestartDockerEngineActivity, env.RestartDockerEngineInput{
			Host: details.Host, Confirmed: true,
		}).Get(restartCtx, nil)
		if err == nil {
			return true, nil
		}
		if temporal.IsCanceledError(err) {
			return true, err
		}
		prompt = fmt.Sprintf("Docker restart on **%s** failed:\n\n```\n%s\n```\n\n"+
			"Retry without restarting, or explicitly confirm another attempt. "+
			"A restart interrupts **all containers and workspaces on that engine**.", details.Host, err)
	}
}

// PerformWithDockerRecovery leaves non-Docker failures to the caller.
func PerformWithDockerRecovery(eCtx ExecContext, actionName string, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	for {
		err := workflow.ExecuteActivity(eCtx, activity, args...).Get(eCtx, valuePtr)
		if err == nil {
			return nil
		}
		handled, recoveryErr := HandleDockerFailure(eCtx, actionName, err)
		if !handled {
			return err
		}
		if recoveryErr != nil {
			return recoveryErr
		}
	}
}
