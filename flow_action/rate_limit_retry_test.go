package flow_action

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
)

func TestRateLimitRetryWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		retryAfter      time.Duration
		priorRateLimits int
		want            time.Duration
	}{
		{name: "no hint starts at a minute", priorRateLimits: 0, want: time.Minute},
		{name: "no hint doubles", priorRateLimits: 1, want: 2 * time.Minute},
		{name: "no hint keeps doubling", priorRateLimits: 5, want: 32 * time.Minute},
		{name: "no hint caps at an hour", priorRateLimits: 6, want: time.Hour},
		{name: "no hint stays capped forever", priorRateLimits: 70, want: time.Hour},
		{name: "hint is trusted on first rejection", retryAfter: 17 * time.Second, priorRateLimits: 0, want: 17 * time.Second},
		{name: "long hint is trusted on first rejection", retryAfter: 3 * time.Hour, priorRateLimits: 0, want: 3 * time.Hour},
		{name: "short hint floored by backoff after repeat", retryAfter: 17 * time.Second, priorRateLimits: 1, want: time.Minute},
		{name: "short hint floored by growing backoff", retryAfter: 17 * time.Second, priorRateLimits: 3, want: 4 * time.Minute},
		{name: "long hint wins over backoff after repeat", retryAfter: 3 * time.Hour, priorRateLimits: 2, want: 3 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, rateLimitRetryWait(tt.retryAfter, tt.priorRateLimits))
		})
	}
}

const rateLimitRetryChildWorkflowId = "rate-limit-retry-child"

type rateLimitRetryParams struct {
	// RateLimits is the number of leading rate limit failures before success.
	RateLimits int32
	RetryAfter time.Duration
	// OtherFailureAfter inserts a non-rate-limit failure after this many
	// rate limits, which the user answers immediately.
	OtherFailureAfter int32
	UserRespond       bool
}

type rateLimitRetryResult struct {
	Value     string
	Requests  []RequestForUser
	ElapsedMs int64
}

func rateLimitRetryChildWorkflow(ctx workflow.Context, params rateLimitRetryParams) (rateLimitRetryResult, error) {
	eCtx := newActivityRetryTestExecContext(ctx, false)
	actionCtx := eCtx.NewActionContext(activityRetryTestActionName)
	start := workflow.Now(ctx)
	var value string
	err := PerformWithUserRetryOptions(actionCtx, UserRetryOptions{OnFailure: &RateLimitFailureHandler{}}, activityRetryTestActivity, &value)
	return rateLimitRetryResult{Value: value, ElapsedMs: workflow.Now(ctx).Sub(start).Milliseconds()}, err
}

func rateLimitRetryParentWorkflow(ctx workflow.Context, params rateLimitRetryParams) (rateLimitRetryResult, error) {
	var result rateLimitRetryResult
	requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req RequestForUser
			requests.Receive(ctx, &req)
			result.Requests = append(result.Requests, req)
			isRateLimit, _ := req.RequestParams["rateLimited"].(bool)
			if params.UserRespond || !isRateLimit {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		}
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: rateLimitRetryChildWorkflowId})
	var childResult rateLimitRetryResult
	err := workflow.ExecuteChildWorkflow(childCtx, rateLimitRetryChildWorkflow, params).Get(ctx, &childResult)
	result.Value = childResult.Value
	result.ElapsedMs = childResult.ElapsedMs
	return result, err
}

func TestRateLimitFailureHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		params        rateLimitRetryParams
		wantCalls     int32
		wantRequests  int
		wantElapsedMs int64
	}{
		{
			name:          "automatic retries back off without user input",
			params:        rateLimitRetryParams{RateLimits: 3},
			wantCalls:     4,
			wantRequests:  3,
			wantElapsedMs: (1 + 2 + 4) * time.Minute.Milliseconds(),
		},
		{
			name:          "provider hint used for the first wait then floored",
			params:        rateLimitRetryParams{RateLimits: 2, RetryAfter: 10 * time.Second},
			wantCalls:     3,
			wantRequests:  2,
			wantElapsedMs: (10 * time.Second).Milliseconds() + time.Minute.Milliseconds(),
		},
		{
			name:          "user retry wins the race",
			params:        rateLimitRetryParams{RateLimits: 2, UserRespond: true},
			wantCalls:     3,
			wantRequests:  2,
			wantElapsedMs: 0,
		},
		{
			name:          "other failure resets the backoff",
			params:        rateLimitRetryParams{RateLimits: 3, OtherFailureAfter: 2},
			wantCalls:     5,
			wantRequests:  4,
			wantElapsedMs: (1 + 2 + 1) * time.Minute.Milliseconds(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage := sqlite.NewTestSqliteStorage(t, "rate-limit-retry")
			service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
			require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
				WorkspaceId: "ws_test",
				Id:          rateLimitRetryChildWorkflowId,
				Status:      "in_progress",
			}))

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			env.RegisterWorkflow(rateLimitRetryChildWorkflow)
			env.RegisterWorkflow(rateLimitRetryParentWorkflow)
			env.RegisterActivity(&FlowActivities{Service: service})
			env.RegisterActivity(&srv.Activities{Service: service})

			var calls, rateLimits int32
			env.OnActivity(activityRetryTestActivity, mock.Anything).Return(
				func(ctx context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					if tt.params.OtherFailureAfter > 0 && atomic.LoadInt32(&rateLimits) == tt.params.OtherFailureAfter && atomic.LoadInt32(&calls) == tt.params.OtherFailureAfter+1 {
						return "", errors.New("simulated failure")
					}
					if atomic.LoadInt32(&rateLimits) < tt.params.RateLimits {
						atomic.AddInt32(&rateLimits, 1)
						return "", common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{
							Provider:   "anthropic",
							RetryAfter: tt.params.RetryAfter,
						}, errors.New("429 Too Many Requests"))
					}
					return "success", nil
				},
			)

			env.ExecuteWorkflow(rateLimitRetryParentWorkflow, tt.params)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var result rateLimitRetryResult
			require.NoError(t, env.GetWorkflowResult(&result))

			assert.Equal(t, "success", result.Value)
			assert.Equal(t, tt.wantCalls, atomic.LoadInt32(&calls))
			assert.Equal(t, tt.wantElapsedMs, result.ElapsedMs)
			require.Len(t, result.Requests, tt.wantRequests)
			for _, req := range result.Requests {
				assert.Equal(t, RequestKindContinue, req.RequestKind)
				assert.Equal(t, "try_again", req.RequestParams["continueTag"])
				if rateLimited, _ := req.RequestParams["rateLimited"].(bool); rateLimited {
					assert.Contains(t, req.Content, "rate limit")
					assert.Contains(t, req.Content, "429 Too Many Requests")
				}
			}

			flow, err := storage.GetFlow(context.Background(), "ws_test", rateLimitRetryChildWorkflowId)
			require.NoError(t, err)
			if !tt.params.UserRespond && tt.params.OtherFailureAfter == 0 {
				assert.Equal(t, "in_progress", flow.Status, "timer-resolved requests must resume the flow")
			}
		})
	}
}
