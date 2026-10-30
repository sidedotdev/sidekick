package flow_action

import (
	"sidekick/domain"
	"time"

	"go.temporal.io/sdk/workflow"
)

type trackedFlowActionKeyType struct{}

// trackedFlowActionCtxKey carries the flow action being tracked by the
// innermost Track, so retry loops running inside it can report progress on
// that same action.
var trackedFlowActionCtxKey = trackedFlowActionKeyType{}

// retryAttemptsChangeId gates persisting retry progress on tracked actions,
// which adds storage activities to the user-retry loop.
const retryAttemptsChangeId = "retry-attempt-metadata"

func withTrackedFlowAction(ctx workflow.Context, flowAction *domain.FlowAction) workflow.Context {
	return workflow.WithValue(ctx, trackedFlowActionCtxKey, flowAction)
}

func trackedFlowAction(ctx workflow.Context) *domain.FlowAction {
	flowAction, _ := ctx.Value(trackedFlowActionCtxKey).(*domain.FlowAction)
	return flowAction
}

// recordRetryAttempt appends a failed attempt to the tracked action, if any,
// and persists it so the attempt is visible while the action is still being
// retried. Any previously scheduled automatic retry is over by now.
func recordRetryAttempt(eCtx ExecContext, attemptErr error) error {
	tracked := trackedFlowAction(eCtx)
	if tracked == nil {
		return nil
	}
	tracked.Attempts = append(tracked.Attempts, domain.FlowActionAttempt{
		At:    workflow.Now(eCtx).UTC(),
		Error: attemptErr.Error(),
	})
	tracked.NextRetryAt = nil
	return persistTrackedFlowAction(eCtx, tracked)
}

// scheduleAutomaticRetry records on the tracked action, if any, that it will
// be retried without user input at the given time.
func scheduleAutomaticRetry(eCtx ExecContext, retryAt time.Time) error {
	tracked := trackedFlowAction(eCtx)
	if tracked == nil {
		return nil
	}
	tracked.NextRetryAt = &retryAt
	return persistTrackedFlowAction(eCtx, tracked)
}

// clearScheduledRetry forgets a scheduled automatic retry on the tracked
// action in memory only, leaving persistence to whoever finalizes the action.
func clearScheduledRetry(eCtx ExecContext) {
	if tracked := trackedFlowAction(eCtx); tracked != nil {
		tracked.NextRetryAt = nil
	}
}

// finishScheduledRetry persists that the scheduled automatic retry is no
// longer pending, because the wait resolved and the next attempt is starting.
func finishScheduledRetry(eCtx ExecContext) error {
	tracked := trackedFlowAction(eCtx)
	if tracked == nil || tracked.NextRetryAt == nil {
		return nil
	}
	tracked.NextRetryAt = nil
	return persistTrackedFlowAction(eCtx, tracked)
}

func persistTrackedFlowAction(eCtx ExecContext, tracked *domain.FlowAction) error {
	persisted, err := putFlowAction(eCtx, *tracked)
	if err != nil {
		return err
	}
	*tracked = persisted
	return nil
}
