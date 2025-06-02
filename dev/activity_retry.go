package dev

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// ExecuteActivityWithUserRetry wraps workflow.ExecuteActivity with automatic user-prompted retry on failure.
// This function is a drop-in replacement for workflow.ExecuteActivity but will prompt the user to
// continue/retry when activities fail, creating an infinite retry loop until the activity succeeds.
// The actionCtx parameter provides the action context for tracking and naming the retry operation.
func ExecuteActivityWithUserRetry(actionCtx DevActionContext, activity interface{}, args ...interface{}) workflow.Future {
	// Create a custom future that handles the retry logic
	future, settable := workflow.NewFuture(actionCtx.DevContext)

	workflow.Go(actionCtx.DevContext, func(gCtx workflow.Context) {
		for {
			// Execute the activity
			activityFuture := workflow.ExecuteActivity(gCtx, activity, args...)

			// Wait for the activity to complete
			var result interface{}
			err := activityFuture.Get(gCtx, &result)

			if err == nil {
				// Activity succeeded, set the result and return
				settable.Set(result, nil)
				return
			}

			// Activity failed, check if we should retry with user prompt
			version := workflow.GetVersion(gCtx, "activity-user-retry", workflow.DefaultVersion, 1)
			if version < 1 {
				// Version doesn't support retry, return the error
				settable.Set(nil, err)
				return
			}

			// Prompt user to retry
			prompt := fmt.Sprintf("%s failed: %s. Would you like to retry?", actionCtx.ActionType, err.Error())
			requestParams := map[string]any{
				"continueTag": "Retry",
			}

			userErr := GetUserContinue(actionCtx, prompt, requestParams)
			if userErr != nil {
				// GetUserContinue failed, return that error and break the retry loop
				settable.Set(nil, userErr)
				return
			}

			// User chose to continue, loop back to retry the activity
		}
	})

	return future
}
