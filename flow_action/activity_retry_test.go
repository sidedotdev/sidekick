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
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"
)

const activityRetryTestActionName = "Test action"

func activityRetryTestActivity(ctx context.Context) (string, error) {
	return "", errors.New("must be mocked")
}

func newActivityRetryTestExecContext(ctx workflow.Context, disableHumanInTheLoop bool) ExecContext {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	gs := &GlobalState{}
	gs.InitValues()
	return ExecContext{
		Context:               ctx,
		WorkspaceId:           "ws_test",
		FlowScope:             &FlowScope{SubflowName: "test-subflow"},
		GlobalState:           gs,
		DisableHumanInTheLoop: disableHumanInTheLoop,
	}
}

// mockFlakyActivity makes the activity fail with "simulated failure" for the
// first `failures` calls and succeed afterwards, returning the call counter.
func mockFlakyActivity(env *testsuite.TestWorkflowEnvironment, failures int32) *int32 {
	var calls int32
	env.OnActivity(activityRetryTestActivity, mock.Anything).Return(
		func(ctx context.Context) (string, error) {
			if atomic.AddInt32(&calls, 1) <= failures {
				return "", errors.New("simulated failure")
			}
			return "success", nil
		},
	)
	return &calls
}

type activityRetryScenarioResult struct {
	Value          string
	Err            string
	Canceled       bool
	PendingAction  bool
	HandledErrs    []string
	HandledActions []string
}

func TestPerformWithUserRetryOptions_HandlerDecisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                  string
		failures              int32
		decisions             []RetryDecision
		handlerErr            error
		disableHumanInTheLoop bool
		pendingAction         bool
		expectedValue         string
		expectedErrContains   string
		expectedCanceled      bool
		expectedPendingAction bool
		expectedHandlerCalls  int
		expectedActivityCalls int32
	}{
		{
			name:                  "retry re-executes the activity",
			failures:              2,
			decisions:             []RetryDecision{RetryDecisionRetry, RetryDecisionRetry},
			expectedValue:         "success",
			expectedHandlerCalls:  2,
			expectedActivityCalls: 3,
		},
		{
			name:                  "cancel returns ErrUserRetryCanceled wrapping the activity error",
			failures:              1,
			decisions:             []RetryDecision{RetryDecisionCancel},
			expectedErrContains:   "simulated failure",
			expectedCanceled:      true,
			expectedHandlerCalls:  1,
			expectedActivityCalls: 1,
		},
		{
			name:                  "fail returns the original activity error",
			failures:              1,
			decisions:             []RetryDecision{RetryDecisionFail},
			expectedErrContains:   "simulated failure",
			expectedHandlerCalls:  1,
			expectedActivityCalls: 1,
		},
		{
			name:                  "handler error is returned as-is",
			failures:              1,
			decisions:             []RetryDecision{RetryDecisionRetry},
			handlerErr:            errors.New("handler boom"),
			expectedErrContains:   "handler boom",
			expectedHandlerCalls:  1,
			expectedActivityCalls: 1,
		},
		{
			name:                  "disabled human in the loop skips the handler",
			failures:              1,
			disableHumanInTheLoop: true,
			expectedErrContains:   "simulated failure",
			expectedActivityCalls: 1,
		},
		{
			name:                  "pending user action skips the handler",
			failures:              1,
			pendingAction:         true,
			expectedPendingAction: true,
			expectedErrContains:   PendingActionError.Error(),
			expectedActivityCalls: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			activityCalls := mockFlakyActivity(env, tc.failures)

			scenario := func(ctx workflow.Context) (activityRetryScenarioResult, error) {
				var result activityRetryScenarioResult
				eCtx := newActivityRetryTestExecContext(ctx, tc.disableHumanInTheLoop)
				if tc.pendingAction {
					eCtx.GlobalState.SetUserAction(UserActionGoNext)
				}
				handler := ActivityFailureHandlerFunc(func(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
					call := len(result.HandledErrs)
					result.HandledErrs = append(result.HandledErrs, activityErr.Error())
					result.HandledActions = append(result.HandledActions, actionName)
					if call >= len(tc.decisions) {
						return RetryDecisionFail, errors.New("unexpected handler call")
					}
					return tc.decisions[call], tc.handlerErr
				})

				actionCtx := eCtx.NewActionContext(activityRetryTestActionName)
				err := PerformWithUserRetryOptions(actionCtx, UserRetryOptions{OnFailure: handler}, activityRetryTestActivity, &result.Value)
				if err != nil {
					result.Err = err.Error()
					result.Canceled = errors.Is(err, ErrUserRetryCanceled)
					result.PendingAction = errors.Is(err, PendingActionError)
				}
				return result, nil
			}
			env.RegisterWorkflow(scenario)

			env.ExecuteWorkflow(scenario)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var result activityRetryScenarioResult
			require.NoError(t, env.GetWorkflowResult(&result))

			assert.Equal(t, tc.expectedValue, result.Value)
			if tc.expectedErrContains == "" {
				assert.Empty(t, result.Err)
			} else {
				assert.Contains(t, result.Err, tc.expectedErrContains)
			}
			assert.Equal(t, tc.expectedCanceled, result.Canceled)
			assert.Equal(t, tc.expectedPendingAction, result.PendingAction)
			require.Len(t, result.HandledErrs, tc.expectedHandlerCalls)
			for i := range result.HandledErrs {
				assert.Contains(t, result.HandledErrs[i], "simulated failure")
				assert.Equal(t, activityRetryTestActionName, result.HandledActions[i])
			}
			assert.Equal(t, tc.expectedActivityCalls, atomic.LoadInt32(activityCalls))
		})
	}
}

const defaultRetryChildWorkflowId = "activity-retry-default-child"

type defaultRetryParentResult struct {
	Value    string
	Requests []RequestForUser
}

func TestPerformWithUserRetryOptions_DefaultHandlerIssuesContinueRequest(t *testing.T) {
	t.Parallel()

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)

	var fa *FlowActivities
	env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	var srvActivities srv.Activities
	env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).
		Return(domain.Flow{WorkspaceId: "ws_test", Id: defaultRetryChildWorkflowId}, nil).Maybe()
	env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
	activityCalls := mockFlakyActivity(env, 1)

	child := func(ctx workflow.Context) (string, error) {
		eCtx := newActivityRetryTestExecContext(ctx, false)
		actionCtx := eCtx.NewActionContext(activityRetryTestActionName)
		var value string
		err := PerformWithUserRetryOptions(actionCtx, UserRetryOptions{}, activityRetryTestActivity, &value)
		return value, err
	}

	parent := func(ctx workflow.Context) (defaultRetryParentResult, error) {
		var result defaultRetryParentResult
		requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var req RequestForUser
				requests.Receive(ctx, &req)
				result.Requests = append(result.Requests, req)
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: defaultRetryChildWorkflowId,
		})
		err := workflow.ExecuteChildWorkflow(childCtx, child).Get(ctx, &result.Value)
		return result, err
	}

	env.RegisterWorkflow(child)
	env.RegisterWorkflow(parent)

	env.ExecuteWorkflow(parent)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result defaultRetryParentResult
	require.NoError(t, env.GetWorkflowResult(&result))

	assert.Equal(t, "success", result.Value)
	assert.Equal(t, int32(2), atomic.LoadInt32(activityCalls))
	require.Len(t, result.Requests, 1)
	req := result.Requests[0]
	assert.Equal(t, RequestKindContinue, req.RequestKind)
	assert.Equal(t, defaultRetryChildWorkflowId, req.OriginWorkflowId)
	assert.Contains(t, req.Content, activityRetryTestActionName+" failed:")
	assert.Contains(t, req.Content, "simulated failure")
	assert.Equal(t, "try_again", req.RequestParams["continueTag"])
}
