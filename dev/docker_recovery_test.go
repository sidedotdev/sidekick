package dev

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
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

func TestDockerFailureHandler(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		approve       bool
		restartErr    error
		ordinaryError bool
		headless      bool
		oldVersion    bool
		runCommand    bool
		wantRequests  int
		wantRestarts  int32
		wantFailure   bool
	}{
		{name: "shell command recovers", runCommand: true, wantRequests: 1},
		{name: "ordinary shell failure stays a failure", runCommand: true, ordinaryError: true, wantFailure: true},
		{name: "legacy shell command stays a failure", runCommand: true, oldVersion: true, wantFailure: true},
		{name: "retry without restart", wantRequests: 1},
		{name: "approved restart", approve: true, wantRequests: 1, wantRestarts: 1},
		{name: "failed restart requires another decision", approve: true, restartErr: errors.New("restart failed"), wantRequests: 2, wantRestarts: 1},
		{name: "canceled restart", approve: true, restartErr: temporal.NewCanceledError(), wantRequests: 1, wantRestarts: 1, wantFailure: true},
		{name: "ordinary error uses fallback", ordinaryError: true, wantRequests: 1},
		{name: "human interaction disabled", headless: true, wantFailure: true},
		{name: "legacy workflow uses fallback", oldVersion: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			if tc.oldVersion {
				version := "docker-recovery-confirmation"
				if tc.runCommand {
					version = "docker-command-recovery"
				}
				testEnv.OnGetVersion(version, workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion)
			}
			if tc.runCommand {
				testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
					Return(CheckCommandPermissionOutput{Result: common.PermissionAutoApprove}, nil).Once()
			}

			var fa *flow_action.FlowActivities
			testEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var srvActivities srv.Activities
			testEnv.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
			testEnv.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

			var attempts, restarts atomic.Int32
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(context.Context, env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					if attempts.Add(1) == 1 {
						failure := temporal.NewNonRetryableApplicationError(
							"Docker unavailable", env.ErrTypeDockerEngineUnavailable, nil,
							env.DockerEngineFailureDetails{Host: "test-host"},
						)
						if tc.ordinaryError {
							failure = errors.New("ordinary failure")
						}
						return env.EnvRunCommandActivityOutput{}, failure
					}
					return env.EnvRunCommandActivityOutput{Stdout: "success"}, nil
				},
			)
			testEnv.OnActivity(env.RestartDockerEngineActivity, mock.Anything, mock.Anything).Return(
				func(ctx context.Context, input env.RestartDockerEngineInput) (env.RestartDockerEngineOutput, error) {
					assert.True(t, input.Confirmed)
					assert.Equal(t, "test-host", input.Host)
					assert.Equal(t, "local-test-queue", activity.GetInfo(ctx).TaskQueue)
					restarts.Add(1)
					return env.RestartDockerEngineOutput{}, tc.restartErr
				},
			).Maybe()

			child := func(ctx workflow.Context) (env.EnvRunCommandActivityOutput, error) {
				ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
					TaskQueue:           "local-test-queue",
					StartToCloseTimeout: time.Minute,
					RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
				})
				gs := &flow_action.GlobalState{}
				gs.InitValues()
				eCtx := flow_action.ExecContext{
					Context:               ctx,
					GlobalState:           gs,
					FlowScope:             &flow_action.FlowScope{SubflowName: "docker-recovery"},
					DisableHumanInTheLoop: tc.headless,
					EnvContainer:          &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "."}},
				}
				if tc.runCommand {
					result, err := RunCommand(DevContext{ExecContext: eCtx}, RunCommandParams{Command: "test command"})
					return env.EnvRunCommandActivityOutput{Stdout: result}, err
				}
				var output env.EnvRunCommandActivityOutput
				err := flow_action.PerformWithUserRetryOptions(
					flow_action.ActionContext{ExecContext: eCtx, ActionType: "Run command"},
					flow_action.UserRetryOptions{
						OnFailure:             DockerFailureHandler{Fallback: flow_action.ContinuePromptFailureHandler{}},
						PropagateCancellation: true,
					}, env.EnvRunCommandActivity, &output, env.EnvRunCommandActivityInput{},
				)
				return output, err
			}
			var requests []flow_action.RequestForUser
			parent := func(ctx workflow.Context) (env.EnvRunCommandActivityOutput, error) {
				signals := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						signals.Receive(ctx, &req)
						requests = append(requests, req)
						approved := tc.approve && len(requests) == 1
						workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
							flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
								FlowActionId: req.FlowActionId, Approved: &approved,
							}).Get(ctx, nil)
					}
				})
				var output env.EnvRunCommandActivityOutput
				err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &output)
				return output, err
			}
			testEnv.RegisterWorkflow(child)
			testEnv.RegisterWorkflow(parent)
			testEnv.ExecuteWorkflow(parent)

			if tc.wantFailure {
				require.Error(t, testEnv.GetWorkflowError())
				assert.Equal(t, int32(1), attempts.Load())
			} else {
				require.NoError(t, testEnv.GetWorkflowError())
				var output env.EnvRunCommandActivityOutput
				require.NoError(t, testEnv.GetWorkflowResult(&output))
				assert.Contains(t, output.Stdout, "success")
				assert.Equal(t, int32(2), attempts.Load())
			}
			assert.Equal(t, tc.wantRestarts, restarts.Load())
			require.Len(t, requests, tc.wantRequests)
			if len(requests) > 0 {
				if tc.ordinaryError || tc.oldVersion {
					assert.Equal(t, flow_action.RequestKindContinue, requests[0].RequestKind)
				} else {
					assert.Equal(t, flow_action.RequestKindApproval, requests[0].RequestKind)
					assert.Contains(t, requests[0].Content, "all containers")
					assert.Equal(t, false, requests[0].RequestParams["rejectReasonRequired"])
				}
			}
			testEnv.AssertExpectations(t)
		})
	}
}
