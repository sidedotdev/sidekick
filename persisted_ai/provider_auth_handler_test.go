package persisted_ai

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
)

const rateLimitHandlerChildWorkflowId = "rate-limit-handler-child"

func rateLimitHandlerTestActivity(ctx context.Context) (string, error) {
	return "", errors.New("must be mocked")
}

type rateLimitHandlerResult struct {
	Value     string
	Requests  []flow_action.RequestForUser
	ElapsedMs int64
}

func rateLimitHandlerChildWorkflow(ctx workflow.Context) (rateLimitHandlerResult, error) {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	eCtx := flow_action.ExecContext{
		Context:     utils.LlmHeartbeatCtx(ctx),
		WorkspaceId: "ws_test",
		FlowScope:   &flow_action.FlowScope{SubflowName: "test-subflow"},
		GlobalState: gs,
	}
	actionCtx := eCtx.NewActionContext("LLM stream")
	start := workflow.Now(ctx)
	var value string
	err := flow_action.PerformWithUserRetryOptions(actionCtx, flow_action.UserRetryOptions{
		OnFailure: NewProviderAuthFailureHandler(),
	}, rateLimitHandlerTestActivity, &value)
	return rateLimitHandlerResult{Value: value, ElapsedMs: workflow.Now(ctx).Sub(start).Milliseconds()}, err
}

// rateLimitHandlerParentWorkflow answers only non-rate-limit continue
// requests, leaving rate limits to the automatic retry.
func rateLimitHandlerParentWorkflow(ctx workflow.Context) (rateLimitHandlerResult, error) {
	var result rateLimitHandlerResult
	requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req flow_action.RequestForUser
			requests.Receive(ctx, &req)
			result.Requests = append(result.Requests, req)
			if rateLimited, _ := req.RequestParams["rateLimited"].(bool); !rateLimited {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		}
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: rateLimitHandlerChildWorkflowId})
	var childResult rateLimitHandlerResult
	err := workflow.ExecuteChildWorkflow(childCtx, rateLimitHandlerChildWorkflow).Get(ctx, &childResult)
	result.Value = childResult.Value
	result.ElapsedMs = childResult.ElapsedMs
	return result, err
}

// TestProviderAuthFailureHandler_RateLimitsSkipActivityRetries runs the
// handler under the real LLM activity retry policy: rate limits must reach the
// workflow after a single attempt and then be retried on the backoff
// schedule, which a generic failure resets.
func TestProviderAuthFailureHandler_RateLimitsSkipActivityRetries(t *testing.T) {
	t.Parallel()

	storage := sqlite.NewTestSqliteStorage(t, "rate-limit-handler")
	service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
	require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
		WorkspaceId: "ws_test",
		Id:          rateLimitHandlerChildWorkflowId,
		Status:      "in_progress",
	}))

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)
	env.RegisterWorkflow(rateLimitHandlerChildWorkflow)
	env.RegisterWorkflow(rateLimitHandlerParentWorkflow)
	env.RegisterActivity(&flow_action.FlowActivities{Service: service})
	env.RegisterActivity(&srv.Activities{Service: service})

	rateLimited := func() error {
		return common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{Provider: "anthropic"}, errors.New("429 Too Many Requests"))
	}
	// Retryable generic failures are retried by Temporal before the workflow
	// sees them, so the reset is exercised with a non-retryable one.
	genericFailure := temporal.NewNonRetryableApplicationError("simulated failure", "Error", nil)
	outcomes := []error{rateLimited(), rateLimited(), genericFailure, rateLimited(), nil}
	var calls int32
	env.OnActivity(rateLimitHandlerTestActivity, mock.Anything).Return(
		func(ctx context.Context) (string, error) {
			call := atomic.AddInt32(&calls, 1)
			require.LessOrEqual(t, int(call), len(outcomes), "unexpected extra activity attempt")
			if err := outcomes[call-1]; err != nil {
				return "", err
			}
			return "success", nil
		},
	)

	env.ExecuteWorkflow(rateLimitHandlerParentWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result rateLimitHandlerResult
	require.NoError(t, env.GetWorkflowResult(&result))

	assert.Equal(t, "success", result.Value)
	assert.Equal(t, int32(len(outcomes)), atomic.LoadInt32(&calls), "each failure must reach the workflow after one attempt")
	// Waits are 1, 2, then 1 minute again since the generic failure resets
	// the backoff, each jittered by up to 25%.
	nominalMs := (1 + 2 + 1) * time.Minute.Milliseconds()
	assert.GreaterOrEqual(t, result.ElapsedMs, nominalMs*3/4)
	assert.LessOrEqual(t, result.ElapsedMs, nominalMs*5/4)
	require.Len(t, result.Requests, 4)
	for i, req := range result.Requests {
		rateLimitedReq, _ := req.RequestParams["rateLimited"].(bool)
		_, wantRateLimited := common.AsProviderRateLimitError(outcomes[i])
		assert.Equal(t, wantRateLimited, rateLimitedReq)
		assert.Equal(t, flow_action.RequestKindContinue, req.RequestKind)
	}
}
