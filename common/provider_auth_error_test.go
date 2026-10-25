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

func TestAsProviderAuthError(t *testing.T) {
	t.Parallel()

	details := ProviderAuthErrorDetails{
		Provider:  "anthropic",
		ProfileId: "work",
		AuthType:  ProviderAuthTypeSubscription,
		Reason:    ProviderAuthErrorReasonInvalidOAuth,
	}
	authErr := NewProviderAuthError(details, errors.New("token refresh failed with status 400"))

	tests := []struct {
		name   string
		err    error
		wantOk bool
	}{
		{name: "direct", err: authErr, wantOk: true},
		{name: "wrapped", err: fmt.Errorf("stream failed: %w", authErr), wantOk: true},
		{name: "nested under other application error", err: temporal.NewApplicationErrorWithCause("outer", "other", authErr), wantOk: true},
		{name: "other application error type", err: temporal.NewNonRetryableApplicationError("x", "other", nil, details)},
		{name: "plain error", err: errors.New("provider auth error")},
		{name: "nil", err: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := AsProviderAuthError(tt.err)
			assert.Equal(t, tt.wantOk, ok)
			if tt.wantOk {
				require.NotNil(t, got)
				assert.Equal(t, details, *got)
			} else {
				assert.Nil(t, got)
			}
		})
	}
}

func TestNewProviderAuthError_IsNonRetryableAndKeepsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("token refresh failed with status 400: invalid_grant")
	err := NewProviderAuthError(ProviderAuthErrorDetails{
		Provider:  "anthropic",
		ProfileId: "work",
		AuthType:  ProviderAuthTypeSubscription,
		Reason:    ProviderAuthErrorReasonInvalidOAuth,
	}, cause)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr))
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, ProviderAuthErrorType, appErr.Type())
	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "invalid_grant")
	assert.Contains(t, err.Error(), "profile work")
}

func TestReattributeProviderAuthError(t *testing.T) {
	t.Parallel()

	cause := errors.New("refresh failed")
	original := NewProviderAuthError(ProviderAuthErrorDetails{
		Provider:  "anthropic",
		ProfileId: "work",
		AuthType:  ProviderAuthTypeSubscription,
		Reason:    ProviderAuthErrorReasonInvalidOAuth,
	}, cause)

	reattributed := ReattributeProviderAuthError(original, "claude", ProviderAuthTypeAny)

	details, ok := AsProviderAuthError(reattributed)
	require.True(t, ok)
	assert.Equal(t, ProviderAuthErrorDetails{
		Provider:  "claude",
		ProfileId: "work",
		AuthType:  ProviderAuthTypeAny,
		Reason:    ProviderAuthErrorReasonInvalidOAuth,
	}, *details)
	assert.ErrorIs(t, reattributed, cause)

	plain := errors.New("keyring locked")
	assert.Same(t, plain, ReattributeProviderAuthError(plain, "claude", ProviderAuthTypeAny))
}

const providerAuthErrorTestActivityName = "ProviderAuthErrorTestActivity"

func providerAuthErrorTestWorkflow(ctx workflow.Context) (ProviderAuthErrorDetails, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5, InitialInterval: time.Millisecond},
	})
	err := workflow.ExecuteActivity(ctx, providerAuthErrorTestActivityName).Get(ctx, nil)
	details, ok := AsProviderAuthError(err)
	if !ok {
		return ProviderAuthErrorDetails{}, fmt.Errorf("expected provider auth error, got: %v", err)
	}
	return *details, nil
}

func TestProviderAuthError_SurvivesActivityBoundary(t *testing.T) {
	t.Parallel()

	want := ProviderAuthErrorDetails{
		Provider:  "openai",
		ProfileId: "work",
		AuthType:  ProviderAuthTypeAPI,
		Reason:    ProviderAuthErrorReasonInvalidAPIKey,
	}
	var attempts int32

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(providerAuthErrorTestWorkflow)
	env.RegisterActivityWithOptions(func(context.Context) error {
		atomic.AddInt32(&attempts, 1)
		return NewProviderAuthError(want, errors.New("401 Unauthorized"))
	}, activity.RegisterOptions{Name: providerAuthErrorTestActivityName})

	env.ExecuteWorkflow(providerAuthErrorTestWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var got ProviderAuthErrorDetails
	require.NoError(t, env.GetWorkflowResult(&got))
	assert.Equal(t, want, got)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
}
