package dev

import (
	"context"
	"sync/atomic"
	"testing"

	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestActivityTimeoutUserRetry(t *testing.T) {
	t.Parallel()
	operations := []struct {
		name string
		run  func(DevContext) error
	}{
		{name: "format", run: AutoFormatCode},
		{name: "checkpoint", run: checkpointSubflowGit},
	}
	scenarios := []struct {
		name            string
		disableHuman    bool
		legacyRetry     bool
		canceled        bool
		cancelOnRequest bool
		wantAttempts    int32
		wantRequests    int
		wantError       string
	}{
		{name: "retry repeated timeouts", wantAttempts: 3, wantRequests: 2},
		{name: "unattended", disableHuman: true, wantAttempts: 1, wantError: "StartToClose"},
		{name: "legacy retry handler", legacyRetry: true, wantAttempts: 1, wantError: "StartToClose"},
		{name: "canceled checkpoint", canceled: true, wantAttempts: 1, wantError: "StartToClose"},
		{name: "cancel pending retry", cancelOnRequest: true, wantAttempts: 1, wantRequests: 1, wantError: "canceled"},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			for _, scenario := range scenarios {
				if scenario.canceled && operation.name != "checkpoint" {
					continue
				}
				t.Run(scenario.name, func(t *testing.T) {
					t.Parallel()
					var suite testsuite.WorkflowTestSuite
					tenv := suite.NewTestWorkflowEnvironment()
					tenv.SetWorkerOptions(utils.TestWorkerOptions())
					if scenario.legacyRetry {
						tenv.OnGetVersion("activity-user-retry", workflow.DefaultVersion, 1).
							Return(workflow.DefaultVersion).Once()
					}

					var fa *flow_action.FlowActivities
					tenv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
						Return(nil).Maybe()
					var activities srv.Activities
					tenv.OnActivity(activities.GetFlow, mock.Anything, mock.Anything, mock.Anything).
						Return(domain.Flow{}, nil).Maybe()
					tenv.OnActivity(activities.PersistFlow, mock.Anything, mock.Anything).
						Return(nil).Maybe()

					var attempts atomic.Int32
					failUntilRetried := func() error {
						if attempts.Add(1) <= 2 {
							return temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil)
						}
						return nil
					}
					if operation.name == "format" {
						tenv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
							Return(func(context.Context, env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
								return env.EnvRunCommandActivityOutput{}, failUntilRetried()
							})
					} else {
						tenv.OnActivity(git.SyncGitActivity, mock.Anything, mock.Anything).
							Return(func(context.Context, git.SyncGitInput) (git.SyncGitOutput, error) {
								return git.SyncGitOutput{}, failUntilRetried()
							})
					}

					childWorkflow := func(ctx workflow.Context) error {
						operationCtx, cancel := workflow.WithCancel(ctx)
						defer cancel()
						if scenario.canceled {
							cancel()
						}
						if scenario.cancelOnRequest {
							workflow.Go(ctx, func(ctx workflow.Context) {
								workflow.GetSignalChannel(ctx, "cancel-operation").Receive(ctx, nil)
								cancel()
							})
						}
						ctx = operationCtx
						gs := &flow_action.GlobalState{}
						gs.InitValues()
						dCtx := DevContext{ExecContext: flow_action.ExecContext{
							Context:               utils.NoRetryCtx(ctx),
							WorkspaceId:           "test-workspace",
							DisableHumanInTheLoop: scenario.disableHuman,
							GlobalState:           gs,
							FlowScope:             &flow_action.FlowScope{SubflowName: "test-subflow"},
							EnvContainer: &env.EnvContainer{Env: &env.ModalEnv{
								WorkingDirectory: "/repo/worktree",
								LocalRepoDir:     "/worker/repo",
								SandboxName:      "sandbox",
							}},
						}}
						return operation.run(dCtx)
					}
					var requests []flow_action.RequestForUser
					parentWorkflow := func(ctx workflow.Context) error {
						signals := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
						workflow.Go(ctx, func(ctx workflow.Context) {
							for {
								var req flow_action.RequestForUser
								signals.Receive(ctx, &req)
								requests = append(requests, req)
								if scenario.cancelOnRequest {
									_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
										"cancel-operation", true).Get(ctx, nil)
									return
								}
								_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
									flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
										FlowActionId: req.FlowActionId,
									}).Get(ctx, nil)
							}
						})
						return workflow.ExecuteChildWorkflow(ctx, childWorkflow).Get(ctx, nil)
					}
					tenv.RegisterWorkflow(childWorkflow)
					tenv.ExecuteWorkflow(parentWorkflow)

					require.True(t, tenv.IsWorkflowCompleted())
					if scenario.wantError != "" {
						require.ErrorContains(t, tenv.GetWorkflowError(), scenario.wantError)
					} else {
						require.NoError(t, tenv.GetWorkflowError())
					}
					require.Equal(t, scenario.wantAttempts, attempts.Load())
					require.Len(t, requests, scenario.wantRequests)
					for _, req := range requests {
						require.Equal(t, flow_action.RequestKindContinue, req.RequestKind)
						require.Equal(t, "try_again", req.RequestParams["continueTag"])
						require.Contains(t, req.Content, "StartToClose")
					}
					tenv.AssertExpectations(t)
				})
			}
		})
	}
}
