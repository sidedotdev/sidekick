package flow_action

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RetryDecision tells the user-retry loop how to proceed after an activity
// failure has been handled.
type RetryDecision int

const (
	// RetryDecisionRetry re-executes the failed activity.
	RetryDecisionRetry RetryDecision = iota
	// RetryDecisionCancel stops retrying and returns ErrUserRetryCanceled
	// wrapping the activity error.
	RetryDecisionCancel
	// RetryDecisionFail stops retrying and returns the original activity error.
	RetryDecisionFail
)

// ErrUserRetryCanceled is returned (wrapping the activity error) when a
// failure handler decides to cancel instead of retrying.
var ErrUserRetryCanceled = errors.New("user retry canceled")

// ActivityFailureHandler decides how to proceed after an activity fails in a
// user-retry loop. The ExecContext embeds the workflow context, so handlers
// may block on user requests, signals or timers. A non-nil error is returned
// to the caller as-is, regardless of the decision.
type ActivityFailureHandler interface {
	HandleActivityFailure(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error)
}

// ActivityFailureHandlerFunc adapts a function to ActivityFailureHandler.
type ActivityFailureHandlerFunc func(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error)

func (f ActivityFailureHandlerFunc) HandleActivityFailure(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
	return f(eCtx, actionName, activityErr)
}

// ContinuePromptFailureHandler shows the activity error in a "Try Again"
// continue prompt and retries once the user continues.
type ContinuePromptFailureHandler struct{}

func (ContinuePromptFailureHandler) HandleActivityFailure(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
	if handled, err := HandleDockerFailure(eCtx, actionName, activityErr); handled {
		if err != nil {
			return RetryDecisionFail, err
		}
		return RetryDecisionRetry, nil
	}
	prompt := fmt.Sprintf("%s failed:\n\n```\n%s\n```", actionName, activityErr.Error())
	requestParams := map[string]any{
		"continueTag": "try_again",
	}
	if err := GetUserContinue(eCtx, prompt, requestParams); err != nil {
		return RetryDecisionFail, err
	}
	return RetryDecisionRetry, nil
}

// UserRetryOptions configures PerformWithUserRetryOptions.
type UserRetryOptions struct {
	// ResolveArgs, when set, rebuilds the activity arguments before every
	// attempt so a retry picks up state changed by OnFailure.
	ResolveArgs func() ([]interface{}, error)
	// OnFailure handles activity failures; nil uses ContinuePromptFailureHandler.
	OnFailure ActivityFailureHandler
	// PropagateCancellation returns activity cancellation errors immediately
	// instead of handling them as failures.
	PropagateCancellation bool
}

// PerformWithUserRetryOptions executes an activity and, on failure, lets
// opts.OnFailure decide whether to retry, cancel or fail.
func PerformWithUserRetryOptions(actionCtx ActionContext, opts UserRetryOptions, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(actionCtx.ExecContext, actionCtx.ActionType, opts, activity, valuePtr, args...)
}

// PerformWithUserRetry executes an activity and, on failure, prompts the user to retry.
// This process repeats until the activity succeeds or the user interaction itself fails.
// The actionCtx parameter provides the action context for tracking and naming the retry operation.
// The valuePtr is a pointer to a variable that will receive the activity's result.
func PerformWithUserRetry(actionCtx ActionContext, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(actionCtx.ExecContext, actionCtx.ActionType, UserRetryOptions{}, activity, valuePtr, args...)
}

// PerformWithUserRetryOrCancel returns activity cancellation to callers that
// distinguish cancellation from ordinary activity failures.
func PerformWithUserRetryOrCancel(actionCtx ActionContext, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(actionCtx.ExecContext, actionCtx.ActionType, UserRetryOptions{PropagateCancellation: true}, activity, valuePtr, args...)
}

// PerformActivity executes an activity once (subject to any automatic retries
// configured via the context's activity options) without prompting the user to
// retry on failure. Callers that have their own fallback should use this so a
// failing activity surfaces as an error instead of blocking on user input.
func PerformActivity(eCtx ExecContext, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return workflow.ExecuteActivity(eCtx, activity, args...).Get(eCtx, valuePtr)
}

// PerformActivityWithUserRetry is like PerformWithUserRetry but takes an
// ExecContext and explicit action name instead of a full ActionContext.
func PerformActivityWithUserRetry(eCtx ExecContext, actionName string, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	return performWithUserRetry(eCtx, actionName, UserRetryOptions{}, activity, valuePtr, args...)
}

func performWithUserRetry(eCtx ExecContext, actionName string, opts UserRetryOptions, activity interface{}, valuePtr interface{}, args ...interface{}) error {
	onFailure := opts.OnFailure
	if onFailure == nil {
		onFailure = ContinuePromptFailureHandler{}
	}

	for {
		attemptArgs := args
		if opts.ResolveArgs != nil {
			resolvedArgs, err := opts.ResolveArgs()
			if err != nil {
				return err
			}
			attemptArgs = resolvedArgs
		}
		// Execute the activity
		activityFuture := workflow.ExecuteActivity(eCtx, activity, attemptArgs...)
		err := activityFuture.Get(eCtx, valuePtr)
		if err == nil {
			return nil
		}
		if opts.PropagateCancellation && temporal.IsCanceledError(err) {
			return err
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

		decision, handlerErr := onFailure.HandleActivityFailure(eCtx, actionName, err)
		if handlerErr != nil {
			return handlerErr
		}
		switch decision {
		case RetryDecisionRetry:
			continue
		case RetryDecisionCancel:
			return fmt.Errorf("%w: %w", ErrUserRetryCanceled, err)
		case RetryDecisionFail:
			return err
		default:
			return fmt.Errorf("unknown retry decision %d: %w", decision, err)
		}
	}
}
