package common

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestNewProviderRateLimitError_IsNonRetryableAndKeepsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New(`429 Too Many Requests {"type":"error","error":{"type":"rate_limit_error"}}`)
	err := NewProviderRateLimitError(ProviderRateLimitErrorDetails{
		Provider:   "anthropic",
		RetryAfter: 90 * time.Second,
	}, cause)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr))
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, ProviderRateLimitErrorType, appErr.Type())
	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "rate_limit_error")
	assert.Contains(t, err.Error(), "anthropic rate limit exceeded, retry after 1m30s")

	details, ok := AsProviderRateLimitError(fmt.Errorf("wrapped: %w", err))
	require.True(t, ok)
	assert.Equal(t, "anthropic", details.Provider)
	assert.Equal(t, 90*time.Second, details.RetryAfter)

	_, ok = AsProviderRateLimitError(NewProviderAuthError(ProviderAuthErrorDetails{Provider: "anthropic"}, cause))
	assert.False(t, ok, "auth errors must not be mistaken for rate limits")
	_, ok = AsProviderRateLimitError(cause)
	assert.False(t, ok)
}

const providerRateLimitErrorTestActivityName = "ProviderRateLimitErrorTestActivity"

func providerRateLimitErrorTestWorkflow(ctx workflow.Context) (ProviderRateLimitErrorDetails, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5, InitialInterval: time.Millisecond},
	})
	err := workflow.ExecuteActivity(ctx, providerRateLimitErrorTestActivityName).Get(ctx, nil)
	details, ok := AsProviderRateLimitError(err)
	if !ok {
		return ProviderRateLimitErrorDetails{}, fmt.Errorf("expected provider rate limit error, got: %v", err)
	}
	return *details, nil
}

func TestProviderRateLimitError_SurvivesActivityBoundaryWithoutRetries(t *testing.T) {
	t.Parallel()

	want := ProviderRateLimitErrorDetails{Provider: "openai", RetryAfter: 6 * time.Minute}
	var attempts int32

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(providerRateLimitErrorTestWorkflow)
	env.RegisterActivityWithOptions(func(context.Context) error {
		atomic.AddInt32(&attempts, 1)
		return NewProviderRateLimitError(want, errors.New("429 Too Many Requests"))
	}, activity.RegisterOptions{Name: providerRateLimitErrorTestActivityName})

	env.ExecuteWorkflow(providerRateLimitErrorTestWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got ProviderRateLimitErrorDetails
	require.NoError(t, env.GetWorkflowResult(&got))
	assert.Equal(t, want, got)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
}
