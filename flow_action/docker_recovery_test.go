package flow_action

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/domain"
	environment "sidekick/env"
	"sidekick/srv"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDockerFailureConfirmation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		restart      bool
		restartFails bool
		oldVersion   bool
		wrapped      bool
		wantRestarts int32
		wantRequests int
	}{
		{name: "retry only", wantRequests: 1},
		{name: "confirmed restart", restart: true, wantRestarts: 1, wantRequests: 1},
		{name: "failed restart then retry only", restart: true, restartFails: true, wantRestarts: 1, wantRequests: 2},
		{name: "old history", oldVersion: true, wantRequests: 1},
		{name: "wrapped preflight retry only", wrapped: true, wantRequests: 1},
		{name: "wrapped preflight confirmed restart", wrapped: true, restart: true, wantRestarts: 1, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			suite := &testsuite.WorkflowTestSuite{}
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			testEnv.SetTestTimeout(10 * time.Second)
			if tc.oldVersion {
				testEnv.OnGetVersion("docker-recovery-confirmation", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion)
			}
			var fa *FlowActivities
			testEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var srvActivities srv.Activities
			testEnv.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).
				Return(domain.Flow{WorkspaceId: "ws_test", Id: defaultRetryChildWorkflowId}, nil).Maybe()
			testEnv.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

			dockerErr := temporal.NewNonRetryableApplicationError("Docker is unavailable",
				environment.ErrTypeDockerEngineUnavailable, nil,
				environment.DockerEngineFailureDetails{Host: "worker-host"})
			if tc.wrapped {
				converter := temporal.GetDefaultFailureConverter()
				dockerErr = converter.FailureToError(converter.ErrorToFailure(
					fmt.Errorf("docker engine not ready for devpod up: %w", dockerErr)))
			}
			testEnv.OnActivity(activityRetryTestActivity, mock.Anything).Return("", dockerErr).Once()
			testEnv.OnActivity(activityRetryTestActivity, mock.Anything).Return("success", nil).Once()
			var restarts atomic.Int32
			testEnv.OnActivity(environment.RestartDockerEngineActivity, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, input environment.RestartDockerEngineInput) (environment.RestartDockerEngineOutput, error) {
					restarts.Add(1)
					assert.Equal(t, "sidekick-docker-restart-worker-host", activity.GetInfo(ctx).TaskQueue)
					assert.Equal(t, "worker-host", input.Host)
					assert.True(t, input.Confirmed)
					if tc.restartFails {
						return environment.RestartDockerEngineOutput{}, errors.New("restart failed")
					}
					return environment.RestartDockerEngineOutput{}, nil
				}).Maybe()

			child := func(ctx workflow.Context) (string, error) {
				eCtx := newActivityRetryTestExecContext(ctx, false)
				var value string
				err := PerformWithUserRetryOptions(eCtx.NewActionContext(activityRetryTestActionName),
					UserRetryOptions{}, activityRetryTestActivity, &value)
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
						approved := tc.restart && len(result.Requests) == 1
						workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
							UserResponseSignalName(req.FlowActionId), UserResponse{
								FlowActionId: req.FlowActionId, Approved: &approved,
							}).Get(ctx, nil)
					}
				})
				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
					WorkflowID: defaultRetryChildWorkflowId,
				})
				err := workflow.ExecuteChildWorkflow(childCtx, child).Get(ctx, &result.Value)
				return result, err
			}
			testEnv.RegisterWorkflow(child)
			testEnv.RegisterWorkflow(parent)
			testEnv.ExecuteWorkflow(parent)
			require.NoError(t, testEnv.GetWorkflowError())
			var result defaultRetryParentResult
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			assert.Equal(t, "success", result.Value)
			assert.Equal(t, tc.wantRestarts, restarts.Load())
			require.Len(t, result.Requests, tc.wantRequests)
			req := result.Requests[0]
			if tc.oldVersion {
				assert.Equal(t, RequestKindContinue, req.RequestKind)
			} else {
				assert.Equal(t, RequestKindApproval, req.RequestKind)
				assert.Equal(t, "restart_docker", req.RequestParams["approveTag"])
				assert.Equal(t, "retry_without_restart", req.RequestParams["rejectTag"])
				assert.Equal(t, false, req.RequestParams["rejectReasonRequired"])
				assert.Contains(t, req.Content, "worker-host")
				assert.Contains(t, req.Content, "all")
			}
			testEnv.AssertExpectations(t)
		})
	}
}
