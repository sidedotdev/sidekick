package flow_action

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
)

const continueTimeoutChildWorkflowId = "continue-timeout-child"

type continueTimeoutParams struct {
	Timeout     time.Duration
	UserRespond bool
}

type continueTimeoutTestResult struct {
	UserResponded   bool
	Requests        []RequestForUser
	Unblocked       int
	ChildDurationMs int64
}

func continueTimeoutChildWorkflow(ctx workflow.Context, params continueTimeoutParams) (continueTimeoutTestResult, error) {
	eCtx := newActivityRetryTestExecContext(ctx, false)
	start := workflow.Now(ctx)
	userResponded, err := GetUserContinueOrTimeout(eCtx, "rate limited", map[string]any{"continueTag": "try_again"}, params.Timeout)
	return continueTimeoutTestResult{
		UserResponded:   userResponded,
		ChildDurationMs: workflow.Now(ctx).Sub(start).Milliseconds(),
	}, err
}

func continueTimeoutParentWorkflow(ctx workflow.Context, params continueTimeoutParams) (continueTimeoutTestResult, error) {
	var result continueTimeoutTestResult
	requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
	unblocked := workflow.GetSignalChannel(ctx, SignalNameSubtaskUnblocked)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req RequestForUser
			requests.Receive(ctx, &req)
			result.Requests = append(result.Requests, req)
			if params.UserRespond {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var sig SubtaskUnblocked
			unblocked.Receive(ctx, &sig)
			result.Unblocked++
		}
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: continueTimeoutChildWorkflowId})
	var childResult continueTimeoutTestResult
	err := workflow.ExecuteChildWorkflow(childCtx, continueTimeoutChildWorkflow, params).Get(ctx, &childResult)
	result.UserResponded = childResult.UserResponded
	result.ChildDurationMs = childResult.ChildDurationMs
	return result, err
}

func TestGetUserContinueOrTimeout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		params            continueTimeoutParams
		wantUserResponded bool
		wantResumedFlow   bool
	}{
		{
			name:              "user responds before the timeout",
			params:            continueTimeoutParams{Timeout: time.Hour, UserRespond: true},
			wantUserResponded: true,
			wantResumedFlow:   false,
		},
		{
			name:              "timeout resolves the request without the user",
			params:            continueTimeoutParams{Timeout: 5 * time.Minute, UserRespond: false},
			wantUserResponded: false,
			wantResumedFlow:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			storage := sqlite.NewTestSqliteStorage(t, "continue-timeout")
			service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
			require.NoError(t, storage.PersistFlow(ctx, domain.Flow{
				WorkspaceId: "ws_test",
				Id:          continueTimeoutChildWorkflowId,
				Status:      "in_progress",
			}))

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			env.RegisterWorkflow(continueTimeoutChildWorkflow)
			env.RegisterWorkflow(continueTimeoutParentWorkflow)
			env.RegisterActivity(&FlowActivities{Service: service})
			env.RegisterActivity(&srv.Activities{Service: service})

			env.ExecuteWorkflow(continueTimeoutParentWorkflow, tc.params)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var result continueTimeoutTestResult
			require.NoError(t, env.GetWorkflowResult(&result))

			assert.Equal(t, tc.wantUserResponded, result.UserResponded)
			require.Len(t, result.Requests, 1)
			assert.Equal(t, RequestKindContinue, result.Requests[0].RequestKind)
			assert.Equal(t, "try_again", result.Requests[0].RequestParams["continueTag"])
			assert.Equal(t, 1, result.Unblocked, "parent must learn the flow is no longer waiting")

			flowAction, err := storage.GetFlowAction(ctx, "ws_test", result.Requests[0].FlowActionId)
			require.NoError(t, err)
			assert.Equal(t, domain.ActionStatusComplete, flowAction.ActionStatus, "the continue request must not stay pending")

			flow, err := storage.GetFlow(ctx, "ws_test", continueTimeoutChildWorkflowId)
			require.NoError(t, err)
			if tc.wantResumedFlow {
				assert.Equal(t, "in_progress", flow.Status)
				assert.GreaterOrEqual(t, result.ChildDurationMs, tc.params.Timeout.Milliseconds())
			} else {
				assert.Equal(t, domain.FlowStatusPaused, flow.Status, "unpausing after a user response is the API's job")
			}
		})
	}
}
