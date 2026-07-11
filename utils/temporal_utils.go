package utils

import (
	"time"

	"github.com/segmentio/ksuid"
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

func RetryCtx(ctx workflow.Context, maxAttempts int32, maxInterval time.Duration) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     3.0,
		MaximumInterval:        maxInterval,
		MaximumAttempts:        maxAttempts,
		NonRetryableErrorTypes: []string{}, // TODO make out-of-bounds errors non-retryable
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         retrypolicy,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	return ctx
}

// ProvisioningRetryCtx returns a context for environment-provisioning
// activities (starting a DevPod workspace, creating an OpenShell sandbox).
// These can hit transient docker/network failures, so they get a small,
// bounded number of automatic retries; once exhausted the failure surfaces
// for user-initiated retry rather than retrying indefinitely. The longer
// timeout accommodates image builds.
func ProvisioningRetryCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    30 * time.Second,
		MaximumAttempts:    3,
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		RetryPolicy:         retrypolicy,
	}
	return workflow.WithActivityOptions(ctx, options)
}

var LlmNumRetries = 4

func LlmHeartbeatCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     5.0,
		MaximumInterval:        20 * time.Second,
		MaximumAttempts:        int32(LlmNumRetries),
		NonRetryableErrorTypes: []string{}, // TODO make out-of-bounds errors non-retryable
	}

	options := workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		HeartbeatTimeout:    20 * time.Second,
		RetryPolicy:         retrypolicy,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	return ctx
}

// LlmBoundedHeartbeatCtx configures LLM activity options with the standard
// heartbeat timeout but a short start-to-close timeout and a small, quick set of
// automatic retries. It's intended for callers that have their own fallback and
// want a failing LLM to surface quickly rather than retrying for a long time.
func LlmBoundedHeartbeatCtx(ctx workflow.Context) workflow.Context {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    3 * time.Second,
		MaximumAttempts:    3,
	}

	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		HeartbeatTimeout:    20 * time.Second,
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

// workflow-safe ksuid generation via a side effect
func KsuidSideEffect(ctx workflow.Context) string {
	v := workflow.GetVersion(ctx, "ksuid-gen-side-effect", workflow.DefaultVersion, 1)
	if v == 1 {
		encodedKsuid := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
			return ksuid.New().String()
		})
		var ksuidValue string
		encodedKsuid.Get(&ksuidValue)
		return ksuidValue
	}

	// non-side-effect to avoid completely breaking old workflows that didn't
	// use side effects for this erroneously - they still mostly work, just with
	// duplicate records sometimes. this is better than forcing them to all be
	// restarted.
	return ksuid.New().String()
}
