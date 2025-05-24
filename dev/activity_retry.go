package dev

import (
	"fmt"
	"reflect"
	"runtime"

	"go.temporal.io/sdk/workflow"
)

// ExecuteActivityWithUserRetry wraps workflow.ExecuteActivity with automatic user-prompted retry on failure.
// This function is a drop-in replacement for workflow.ExecuteActivity but will prompt the user to
// continue/retry when activities fail, creating an infinite retry loop until the activity succeeds.
func ExecuteActivityWithUserRetry(ctx DevContext, activity interface{}, args ...interface{}) workflow.Future {
	// Create a custom future that handles the retry logic
	future, settable := workflow.NewFuture(ctx)
	
	workflow.Go(ctx, func(gCtx workflow.Context) {
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
			
			// Get activity name for error message
			activityName := getActivityName(activity)
			
			// Create action context for user retry prompt
			actionCtx := ctx.NewActionContext("activity_retry")
			
			// Prompt user to retry
			prompt := fmt.Sprintf("%s failed: %s. Would you like to retry?", activityName, err.Error())
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

// getActivityName extracts a readable name from the activity interface{}
func getActivityName(activity interface{}) string {
	if activity == nil {
		return "Unknown Activity"
	}
	
	// Try to get the function name using reflection
	activityType := reflect.TypeOf(activity)
	if activityType.Kind() == reflect.Func {
		// Get the function name
		if funcName := runtime.FuncForPC(reflect.ValueOf(activity).Pointer()).Name(); funcName != "" {
			// Extract just the function name from the full path
			if lastDot := len(funcName) - 1; lastDot >= 0 {
				for i := lastDot; i >= 0; i-- {
					if funcName[i] == '.' {
						return funcName[i+1:]
					}
				}
			}
			return funcName
		}
	}
	
	// Fallback to type name
	return activityType.String()
}