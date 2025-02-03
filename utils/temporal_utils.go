package utils

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func DefaultRetryCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     3.0,
		MaximumInterval:        10 * time.Second,
		MaximumAttempts:        3,          // up to 3 retries
		NonRetryableErrorTypes: []string{}, // TODO make out-of-bounds errors non-retryable
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         retrypolicy,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	return ctx
}

func LlmHeartbeatCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     5.0,
		MaximumInterval:        20 * time.Second,
		MaximumAttempts:        4,          // up to 4 retries
		NonRetryableErrorTypes: []string{}, // TODO make out-of-bounds errors non-retryable
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    120 * time.Second, // we heartbeat every 120s: 5s was not enough for anthropic tool streaming chunks sometimes and 60s not enough for o3-mini. maybe incorporating ping message events will fix this for anthropic.
		RetryPolicy:         retrypolicy,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	return ctx
}

func NoRetryCtx(ctx workflow.Context) workflow.Context {
	noRetryPolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        10 * time.Second,
		MaximumAttempts:        1, // no retries
		NonRetryableErrorTypes: []string{"SomeApplicationError", "AnotherApplicationError"},
	}
	noRetryOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         noRetryPolicy,
	}
	//localNoRetryOptions.TaskQueue
	//noRetryOptions.TaskQueue
	noRetryCtx := workflow.WithActivityOptions(ctx, noRetryOptions)
	return noRetryCtx
}

func SingleRetryCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        500 * time.Millisecond,
		MaximumAttempts:        2,
		NonRetryableErrorTypes: []string{},
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         retrypolicy,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	return ctx
}
