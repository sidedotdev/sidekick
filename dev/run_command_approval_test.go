package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func mockCommandBookkeeping(testEnv *testsuite.TestWorkflowEnvironment) {
	var fa *flow_action.FlowActivities
	testEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	var activities srv.Activities
	testEnv.OnActivity(activities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	testEnv.OnActivity(activities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func TestRunCommandSwitchDuringApproval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		changePolicy    bool
		deny            bool
		reject          bool
		cancel          bool
		wantPermissions int
		wantSchedules   int
	}{
		{name: "local approval survives switch", wantPermissions: 1, wantSchedules: 2},
		{name: "approval survives reevaluated approval policy", changePolicy: true, wantPermissions: 2, wantSchedules: 1},
		{name: "new denial overrides approval", changePolicy: true, deny: true, wantPermissions: 2},
		{name: "rejected approval terminates", reject: true, wantPermissions: 1},
		{name: "cancellation terminates approval", cancel: true, wantPermissions: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			boundary := &commandDispatchTestInterceptor{}
			options := utils.TestWorkerOptions()
			options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor(), boundary)
			testEnv.SetWorkerOptions(options)
			mockCommandBookkeeping(testEnv)

			permissionCalls, schedules := 0, 0
			var destinations []string
			boundary.beforeDispatch = func(workflow.Context) { schedules++ }
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input CheckCommandPermissionInput) (CheckCommandPermissionOutput, error) {
					permissionCalls++
					outcome := common.PermissionRequireApproval
					if len(input.CommandPermissions.Deny) != 0 {
						outcome = common.PermissionDeny
					}
					return CheckCommandPermissionOutput{
						Result: outcome, Message: "destination policy",
						Evaluation: &common.ScriptPermissionEvaluation{Outcome: outcome},
					}, nil
				},
			)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					destinations = append(destinations, input.EnvContainer.Env.GetWorkingDirectory())
					return env.EnvRunCommandActivityOutput{Stdout: "success"}, nil
				},
			).Maybe()

			child := func(ctx workflow.Context) (string, error) {
				ctx = utils.NoRetryCtx(ctx)
				commandCtx, cancel := workflow.WithCancel(ctx)
				defer cancel()
				container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/source"}}
				gs := &flow_action.GlobalState{}
				state := newFlowEnvState(gs)
				state.publish(&container)
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: env.WithEnvTracker(commandCtx, state), GlobalState: gs,
					EnvContainer: &container, FlowScope: &flow_action.FlowScope{},
				}}
				workflow.Go(ctx, func(ctx workflow.Context) {
					var unused bool
					workflow.GetSignalChannel(ctx, "switch").Receive(ctx, &unused)
					state.setEnvSwitch(envSwitchState{From: "local", To: "local_git_worktree"})
					container = env.EnvContainer{Env: &env.LocalGitWorktreeEnv{WorkingDirectory: "/destination"}}
					state.publish(&container)
					if tc.changePolicy {
						policy := common.CommandPermissionConfig{
							RequireApproval: []common.CommandPattern{{Pattern: "echo"}},
						}
						if tc.deny {
							policy.Deny = []common.CommandPattern{{Pattern: "echo"}}
						}
						gs.SetValue(globalStateKeyCommandPermissions, policy)
					}
					state.clearEnvSwitch()
					parentID := workflow.GetInfo(ctx).ParentWorkflowExecution.ID
					_ = workflow.SignalExternalWorkflow(ctx, parentID, "", "switched", true).Get(ctx, nil)
					if tc.cancel {
						cancel()
					}
				})
				return RunCommand(dCtx, RunCommandParams{Command: "echo hello"})
			}
			var requests []flow_action.RequestForUser
			parent := func(ctx workflow.Context) (string, error) {
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser).Receive(ctx, &req)
						requests = append(requests, req)
						if len(requests) == 1 {
							_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", "switch", true).Get(ctx, nil)
							var switched bool
							workflow.GetSignalChannel(ctx, "switched").Receive(ctx, &switched)
						}
						if !tc.cancel {
							approved := !tc.reject
							_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
								flow_action.UserResponseSignalName(req.FlowActionId),
								flow_action.UserResponse{Approved: &approved, Content: "decision"}).Get(ctx, nil)
						}
					}
				})
				var result string
				err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &result)
				return result, err
			}
			testEnv.RegisterWorkflow(child)
			testEnv.ExecuteWorkflow(parent)
			if tc.cancel {
				require.True(t, temporal.IsCanceledError(testEnv.GetWorkflowError()))
			} else {
				require.NoError(t, testEnv.GetWorkflowError())
				var result string
				require.NoError(t, testEnv.GetWorkflowResult(&result))
				switch {
				case tc.deny:
					require.Contains(t, result, "Command denied")
				case tc.reject:
					require.Contains(t, result, "not approved")
				default:
					require.Contains(t, result, "success")
					require.Equal(t, []string{"/destination"}, destinations)
				}
			}
			if tc.deny || tc.reject || tc.cancel {
				require.Empty(t, destinations)
			}
			require.Len(t, requests, 1)
			require.Equal(t, tc.wantPermissions, permissionCalls)
			require.Equal(t, tc.wantSchedules, schedules)
			testEnv.AssertExpectations(t)
		})
	}
}

func TestRunCommandGuardsDockerRedispatch(t *testing.T) {
	t.Parallel()
	for _, deny := range []bool{false, true} {
		name := "redispatch becomes stale"
		if deny {
			name = "destination denies retry"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			boundary := &commandDispatchTestInterceptor{}
			options := utils.TestWorkerOptions()
			options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor(), boundary)
			testEnv.SetWorkerOptions(options)
			mockCommandBookkeeping(testEnv)

			permissionCalls, restartCalls, schedules := 0, 0, 0
			var destinations []string
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).Return(
				func(context.Context, CheckCommandPermissionInput) (CheckCommandPermissionOutput, error) {
					permissionCalls++
					outcome := common.PermissionAutoApprove
					if deny {
						outcome = common.PermissionDeny
					}
					return CheckCommandPermissionOutput{
						Result: outcome, Message: "local policy",
						Evaluation: &common.ScriptPermissionEvaluation{Outcome: outcome},
					}, nil
				},
			)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					destinations = append(destinations, input.EnvContainer.Env.GetWorkingDirectory())
					if len(destinations) == 1 {
						return env.EnvRunCommandActivityOutput{}, temporal.NewNonRetryableApplicationError(
							"Docker unavailable", env.ErrTypeDockerEngineUnavailable, nil,
							env.DockerEngineFailureDetails{Host: "test-host"},
						)
					}
					return env.EnvRunCommandActivityOutput{Stdout: "success"}, nil
				},
			)
			testEnv.OnActivity(env.RestartDockerEngineActivity, mock.Anything, mock.Anything).Return(
				func(context.Context, env.RestartDockerEngineInput) (env.RestartDockerEngineOutput, error) {
					restartCalls++
					return env.RestartDockerEngineOutput{}, nil
				},
			).Once()
			child := func(ctx workflow.Context) (string, error) {
				ctx = utils.NoRetryCtx(ctx)
				container := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/sandbox"}}
				gs := &flow_action.GlobalState{}
				state := newFlowEnvState(gs)
				state.publish(&container)
				ctx = env.WithEnvTracker(ctx, state)
				boundary.beforeDispatch = func(workflow.Context) {
					schedules++
					if schedules == 2 {
						state.setEnvSwitch(envSwitchState{From: "local", To: "local"})
						workflow.Go(ctx, func(ctx workflow.Context) {
							_ = workflow.Sleep(ctx, time.Second)
							container = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/after-recovery-switch"}}
							state.publish(&container)
							gs.SetValue(globalStateKeyCommandPermissions, common.CommandPermissionConfig{
								AutoApprove: []common.CommandPattern{{Pattern: "echo"}},
							})
							state.clearEnvSwitch()
						})
					}
				}
				workflow.Go(ctx, func(ctx workflow.Context) {
					var unused bool
					workflow.GetSignalChannel(ctx, "switch").Receive(ctx, &unused)
					container = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/local"}}
					state.publish(&container)
					parentID := workflow.GetInfo(ctx).ParentWorkflowExecution.ID
					_ = workflow.SignalExternalWorkflow(ctx, parentID, "", "switched", true).Get(ctx, nil)
				})
				return RunCommand(DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, GlobalState: gs, EnvContainer: &container,
					FlowScope: &flow_action.FlowScope{},
				}}, RunCommandParams{Command: "echo hello"})
			}
			var requests []flow_action.RequestForUser
			parent := func(ctx workflow.Context) (string, error) {
				workflow.Go(ctx, func(ctx workflow.Context) {
					var req flow_action.RequestForUser
					workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser).Receive(ctx, &req)
					requests = append(requests, req)
					_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", "switch", true).Get(ctx, nil)
					var switched bool
					workflow.GetSignalChannel(ctx, "switched").Receive(ctx, &switched)
					approved := true
					_ = workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
						flow_action.UserResponseSignalName(req.FlowActionId),
						flow_action.UserResponse{Approved: &approved}).Get(ctx, nil)
				})
				var result string
				err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &result)
				return result, err
			}
			testEnv.RegisterWorkflow(child)
			testEnv.ExecuteWorkflow(parent)
			require.NoError(t, testEnv.GetWorkflowError())
			require.Len(t, requests, 1, "stale rejection must not enter Docker recovery")
			require.Equal(t, 1, restartCalls)
			var result string
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			if deny {
				require.Contains(t, result, "Command denied")
				require.Equal(t, []string{"/sandbox"}, destinations)
				require.Equal(t, 1, permissionCalls)
				require.Equal(t, 1, schedules)
			} else {
				require.Contains(t, result, "success")
				require.Equal(t, []string{"/sandbox", "/after-recovery-switch"}, destinations)
				require.Equal(t, 2, permissionCalls)
				require.Equal(t, 3, schedules)
			}
			testEnv.AssertExpectations(t)
		})
	}
}
