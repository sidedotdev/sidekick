package flow_action

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// PerformWithUserRetry executes an activity and, on failure, prompts the user to retry.
// This process repeats until the activity succeeds or the user interaction itself fails.
// The actionCtx parameter provides the action context for tracking and naming the retry operation.
// The valuePtr is a pointer to a variable that will receive the activity's result.
func PerformWithUserRetry(actionCtx ActionContext, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(actionCtx.ExecContext, actionCtx.ActionType, activity, valuePtr, args...)
}

// PerformActivityWithUserRetry is like PerformWithUserRetry but takes an
// ExecContext and explicit action name instead of a full ActionContext.
func PerformActivityWithUserRetry(eCtx ExecContext, actionName string, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(eCtx, actionName, activity, valuePtr, args...)
}

func performWithUserRetry(eCtx ExecContext, actionName string, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	for {
		// Execute the activity
		activityFuture := workflow.ExecuteActivity(eCtx, activity, args...)
		err := activityFuture.Get(eCtx, valuePtr)
		if err == nil {
			return nil
		}

		// Activity failed, check if we should retry with user prompt based on workflow version
		// The version "activity-user-retry" with a change ID of 1 enables this feature.
		// Workflows started before this version was introduced will use workflow.DefaultVersion.
		version := workflow.GetVersion(eCtx, "activity-user-retry", workflow.DefaultVersion, 1)
		if version < 1 {
			// Version doesn't support user-prompted retry for this activity, return the original error
			return err
		}

		// If human-in-the-loop is disabled, don't retry to prevent infinite loops
		if eCtx.DisableHumanInTheLoop {
			return err
		}

		// pending user actions take precedence over the retry loop
		if v := workflow.GetVersion(eCtx, "user-action-go-next", workflow.DefaultVersion, 1); v == 1 {
			action := eCtx.GlobalState.GetPendingUserAction()
			if action != nil {
				return PendingActionError
			}
		}

		// Activity failed and version supports retry, prompt user to retry
		prompt := fmt.Sprintf("%s failed:\n\n```\n%s\n```", actionName, err.Error())
		requestParams := map[string]any{
			"continueTag": "try_again",
		}
		userErr := GetUserContinue(eCtx, prompt, requestParams)
		if userErr != nil {
			// GetUserContinue failed, return that error and break the retry loop
			return userErr
		}

		// User chose to continue, loop back to retry the activity
	}
}
