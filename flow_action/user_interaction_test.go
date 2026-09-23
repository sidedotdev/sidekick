package flow_action

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"
)

// runReceiveUserResponseWorkflow drives ReceiveUserResponse from a workflow
// context for testing.
func runReceiveUserResponseWorkflow(expectedFlowActionId string) func(ctx workflow.Context) (UserResponse, error) {
	return func(ctx workflow.Context) (UserResponse, error) {
		return ReceiveUserResponse(ctx, expectedFlowActionId)
	}
}

func TestReceiveUserResponse_OnlyReceivesSignalForOwnFlowActionId(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)

	expected := "action-current"
	approved := true

	// Stale signal targeted at a different flow action's signal name; the
	// workflow listens on a different channel and must not consume it.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(UserResponseSignalName("action-stale"), UserResponse{
			FlowActionId: "action-stale",
			Approved:     &approved,
			Content:      "stale",
		})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(UserResponseSignalName(expected), UserResponse{
			FlowActionId: expected,
			Approved:     &approved,
			Content:      "matched",
		})
	}, 2*time.Millisecond)

	env.ExecuteWorkflow(runReceiveUserResponseWorkflow(expected))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result UserResponse
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, expected, result.FlowActionId)
	assert.Equal(t, "matched", result.Content)
}

func TestUserResponseSignalName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SignalNameUserResponse, UserResponseSignalName(""))
	assert.Equal(t, SignalNameUserResponse+"-abc123", UserResponseSignalName("abc123"))
}

func TestReceiveUserResponse_AcceptsAnyWhenExpectedIsEmpty(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)

	approved := true
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameUserResponse, UserResponse{
			FlowActionId: "anything",
			Approved:     &approved,
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(runReceiveUserResponseWorkflow(""))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result UserResponse
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, "anything", result.FlowActionId)
}

const skipOptionsFlowActionId = "action-skip-options"
const skipOptionsChildWorkflowId = "user-request-skip-child"

type userRequestSkipParams struct {
	SkipPauseFlow         bool
	SkipParentSignal      bool
	SignalParentViaHelper bool
}

type userRequestSkipResult struct {
	ParentRequests []RequestForUser
	Response       *UserResponse
}

func userRequestSkipChildWorkflow(ctx workflow.Context, params userRequestSkipParams) (*UserResponse, error) {
	eCtx := ExecContext{
		Context:     ctx,
		WorkspaceId: "ws_test",
		FlowScope:   &FlowScope{SubflowName: "test-subflow"},
	}
	req := RequestForUser{
		FlowActionId:     skipOptionsFlowActionId,
		Content:          "Please review these changes",
		RequestKind:      RequestKindMergeApproval,
		SkipPauseFlow:    params.SkipPauseFlow,
		SkipParentSignal: params.SkipParentSignal,
	}
	if params.SignalParentViaHelper {
		if err := SignalParentRequestForUser(eCtx, req); err != nil {
			return nil, err
		}
	}
	return GetUserResponse(eCtx, req)
}

// userRequestSkipParentWorkflow records the requests the child workflow sends
// its way, then responds to the child's request so it can complete.
func userRequestSkipParentWorkflow(ctx workflow.Context, params userRequestSkipParams) (userRequestSkipResult, error) {
	var result userRequestSkipResult
	requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req RequestForUser
			requests.Receive(ctx, &req)
			result.ParentRequests = append(result.ParentRequests, req)
		}
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: skipOptionsChildWorkflowId,
	})
	child := workflow.ExecuteChildWorkflow(childCtx, userRequestSkipChildWorkflow, params)
	var execution workflow.Execution
	if err := child.GetChildWorkflowExecution().Get(ctx, &execution); err != nil {
		return result, err
	}

	// give the child time to raise its request before responding to it
	if err := workflow.Sleep(ctx, time.Second); err != nil {
		return result, err
	}

	approved := true
	response := UserResponse{
		FlowActionId: skipOptionsFlowActionId,
		Approved:     &approved,
		Content:      "approved",
	}
	signalName := UserResponseSignalName(skipOptionsFlowActionId)
	if err := workflow.SignalExternalWorkflow(ctx, execution.ID, execution.RunID, signalName, response).Get(ctx, nil); err != nil {
		return result, err
	}

	var childResponse *UserResponse
	if err := child.Get(ctx, &childResponse); err != nil {
		return result, err
	}
	result.Response = childResponse
	return result, nil
}

func TestGetUserResponse_SkipParentSignalAndPauseOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                   string
		params                 userRequestSkipParams
		expectedParentRequests int
		expectFlowPaused       bool
	}{
		{
			name:                   "defaults signal parent and pause flow",
			params:                 userRequestSkipParams{},
			expectedParentRequests: 1,
			expectFlowPaused:       true,
		},
		{
			name:                   "skip options avoid parent signal and pause",
			params:                 userRequestSkipParams{SkipPauseFlow: true, SkipParentSignal: true},
			expectedParentRequests: 0,
			expectFlowPaused:       false,
		},
		{
			name:                   "parent signaled on demand via helper",
			params:                 userRequestSkipParams{SkipPauseFlow: true, SkipParentSignal: true, SignalParentViaHelper: true},
			expectedParentRequests: 1,
			expectFlowPaused:       false,
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
			env.RegisterWorkflow(userRequestSkipChildWorkflow)

			var srvActivities srv.Activities
			var getFlowCalls, persistFlowCalls int32
			env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).
				Return(domain.Flow{WorkspaceId: "ws_test", Id: skipOptionsChildWorkflowId}, nil).
				Run(func(args mock.Arguments) {
					atomic.AddInt32(&getFlowCalls, 1)
				}).Maybe()
			env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).
				Return(nil).
				Run(func(args mock.Arguments) {
					atomic.AddInt32(&persistFlowCalls, 1)
				}).Maybe()

			env.ExecuteWorkflow(userRequestSkipParentWorkflow, tc.params)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var result userRequestSkipResult
			require.NoError(t, env.GetWorkflowResult(&result))

			require.NotNil(t, result.Response)
			assert.Equal(t, "approved", result.Response.Content)

			require.Len(t, result.ParentRequests, tc.expectedParentRequests)
			if tc.expectedParentRequests > 0 {
				assert.Equal(t, RequestKindMergeApproval, result.ParentRequests[0].RequestKind)
				assert.Equal(t, skipOptionsChildWorkflowId, result.ParentRequests[0].OriginWorkflowId)
				assert.Equal(t, skipOptionsFlowActionId, result.ParentRequests[0].FlowActionId)
			}

			if tc.expectFlowPaused {
				assert.Equal(t, int32(1), atomic.LoadInt32(&getFlowCalls))
				assert.Equal(t, int32(1), atomic.LoadInt32(&persistFlowCalls))
			} else {
				assert.Zero(t, atomic.LoadInt32(&getFlowCalls))
				assert.Zero(t, atomic.LoadInt32(&persistFlowCalls))
			}
		})
	}
}
