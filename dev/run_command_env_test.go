package dev

import (
	"context"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type commandDispatchTestInterceptor struct {
	interceptor.WorkerInterceptorBase
	beforeDispatch func(workflow.Context)
}

func (i *commandDispatchTestInterceptor) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &commandDispatchTestInbound{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
		owner:                          i,
	}
}

type commandDispatchTestInbound struct {
	interceptor.WorkflowInboundInterceptorBase
	owner *commandDispatchTestInterceptor
}

func (i *commandDispatchTestInbound) Init(next interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&commandDispatchTestOutbound{
		WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: next},
		owner:                           i.owner,
	})
}

type commandDispatchTestOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
	owner *commandDispatchTestInterceptor
}

func (i *commandDispatchTestOutbound) ExecuteActivity(ctx workflow.Context, name string, args ...interface{}) workflow.Future {
	if strings.HasSuffix(name, "EnvRunCommandActivity") && i.owner.beforeDispatch != nil {
		i.owner.beforeDispatch(ctx)
	}
	return i.Next.ExecuteActivity(ctx, name, args...)
}

func TestRunCommandEnvironmentPreparation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		sandbox         bool
		deny            bool
		switching       bool
		failedSwitch    bool
		cancel          bool
		policyChanged   bool
		replacement     bool
		wantPermissions int
		wantDispatches  int
	}{
		{name: "sandbox bypass to local denial", sandbox: true, deny: true, wantPermissions: 1},
		{name: "sandbox bypass to checked local", sandbox: true, wantPermissions: 1, wantDispatches: 1},
		{name: "unchanged local policy is reused", wantPermissions: 1, wantDispatches: 1},
		{name: "changed local policy is reevaluated", policyChanged: true, wantPermissions: 2, wantDispatches: 1},
		{name: "switch in progress waits", switching: true, wantPermissions: 1, wantDispatches: 1},
		{name: "failed switch retains authorization", switching: true, failedSwitch: true, wantPermissions: 1, wantDispatches: 1},
		{name: "cancellation terminates wait", switching: true, cancel: true, wantPermissions: 1},
		{name: "replacement sandbox retains bypass", sandbox: true, replacement: true, wantDispatches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			boundary := &commandDispatchTestInterceptor{}
			options := utils.TestWorkerOptions()
			options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor(), boundary)
			testEnv.SetWorkerOptions(options)

			permissionCalls, commandCalls, dispatchAttempts := 0, 0, 0
			var dispatchedDir, dispatchedSandbox string
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).Return(
				func(context.Context, CheckCommandPermissionInput) (CheckCommandPermissionOutput, error) {
					permissionCalls++
					result := common.PermissionAutoApprove
					if tc.deny {
						result = common.PermissionDeny
					}
					return CheckCommandPermissionOutput{
						Result: result, Message: "policy",
						Evaluation: &common.ScriptPermissionEvaluation{Outcome: result},
					}, nil
				},
			).Maybe()
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					commandCalls++
					dispatchedDir = input.EnvContainer.Env.GetWorkingDirectory()
					dispatchedSandbox = env.InstanceOf(input.EnvContainer).SandboxName
					return env.EnvRunCommandActivityOutput{Stdout: "success"}, nil
				},
			).Maybe()
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				ctx, cancel := workflow.WithCancel(utils.NoRetryCtx(ctx))
				defer cancel()
				container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/source"}}
				if tc.sandbox {
					container.Env = &env.DevPodEnv{WorkingDirectory: "/source", WorkspaceName: "original"}
				}
				gs := &flow_action.GlobalState{}
				state := newFlowEnvState(gs)
				state.publish(&container)
				ctx = env.WithEnvTracker(ctx, state)
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, GlobalState: gs, EnvContainer: &container,
				}}
				publish := func() {
					if !tc.failedSwitch {
						container = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/destination"}}
						if tc.replacement {
							container.Env = &env.DevPodEnv{WorkingDirectory: "/source", WorkspaceName: "replacement"}
						}
						state.publish(&container)
					}
					if tc.policyChanged {
						gs.SetValue(globalStateKeyCommandPermissions, common.CommandPermissionConfig{
							AutoApprove: []common.CommandPattern{{Pattern: "echo"}},
						})
					}
					state.clearEnvSwitch()
				}
				boundary.beforeDispatch = func(workflow.Context) {
					dispatchAttempts++
					if dispatchAttempts > 1 {
						return
					}
					if !tc.switching {
						publish()
						return
					}
					state.setEnvSwitch(envSwitchState{From: "local", To: "local"})
					workflow.Go(ctx, func(gCtx workflow.Context) {
						_ = workflow.Sleep(gCtx, time.Second)
						if tc.cancel {
							cancel()
						} else {
							publish()
						}
					})
				}
				return RunCommand(dCtx, RunCommandParams{Command: "echo hello"})
			})
			if tc.cancel {
				require.Error(t, testEnv.GetWorkflowError())
				require.True(t, temporal.IsCanceledError(testEnv.GetWorkflowError()))
			} else {
				require.NoError(t, testEnv.GetWorkflowError())
				var result string
				require.NoError(t, testEnv.GetWorkflowResult(&result))
				if tc.deny {
					require.Contains(t, result, "Command denied")
				} else {
					require.Contains(t, result, "success")
				}
			}
			require.Equal(t, tc.wantPermissions, permissionCalls)
			require.Equal(t, tc.wantDispatches, commandCalls)
			if tc.wantDispatches > 0 {
				wantDir := "/destination"
				if tc.failedSwitch || tc.replacement {
					wantDir = "/source"
				}
				require.Equal(t, wantDir, dispatchedDir)
				require.Equal(t, 2, dispatchAttempts, "stale dispatch must be rejected before rescheduling")
			}
			if tc.replacement {
				require.Equal(t, "replacement", dispatchedSandbox)
			}
		})
	}
}

func TestRunCommandSwitchDuringPermissionEvaluation(t *testing.T) {
	t.Parallel()
	for _, policyOnly := range []bool{false, true} {
		name := "environment and policy"
		if policyOnly {
			name = "policy only"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			options := utils.TestWorkerOptions()
			options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor())
			testEnv.SetWorkerOptions(options)
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything,
				mock.MatchedBy(func(input CheckCommandPermissionInput) bool {
					return len(input.CommandPermissions.Deny) == 0
				})).After(2*time.Second).Return(CheckCommandPermissionOutput{
				Result:     common.PermissionAutoApprove,
				Evaluation: &common.ScriptPermissionEvaluation{Outcome: common.PermissionAutoApprove},
			}, nil).Once()
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything,
				mock.MatchedBy(func(input CheckCommandPermissionInput) bool {
					return len(input.CommandPermissions.Deny) > 0
				})).Return(CheckCommandPermissionOutput{
				Result: common.PermissionDeny, Message: "destination denies",
				Evaluation: &common.ScriptPermissionEvaluation{Outcome: common.PermissionDeny},
			}, nil).Once()
			commandCalls := 0
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(context.Context, env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					commandCalls++
					return env.EnvRunCommandActivityOutput{}, nil
				},
			).Maybe()
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				ctx = utils.NoRetryCtx(ctx)
				container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/source"}}
				gs := &flow_action.GlobalState{}
				state := newFlowEnvState(gs)
				state.publish(&container)
				ctx = env.WithEnvTracker(ctx, state)
				workflow.Go(ctx, func(ctx workflow.Context) {
					_ = workflow.Sleep(ctx, time.Second)
					if !policyOnly {
						container = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/destination"}}
						state.publish(&container)
					}
					gs.SetValue(globalStateKeyCommandPermissions, common.CommandPermissionConfig{
						Deny: []common.CommandPattern{{Pattern: "echo"}},
					})
				})
				return RunCommand(DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, EnvContainer: &container, GlobalState: gs,
				}}, RunCommandParams{Command: "echo hello"})
			})
			require.NoError(t, testEnv.GetWorkflowError())
			var result string
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			require.Contains(t, result, "Command denied")
			require.Zero(t, commandCalls)
			testEnv.AssertExpectations(t)
		})
	}
}

func TestRunCommandResolvesBeforeAuthorizing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		version    workflow.Version
		checkError bool
	}{
		{name: "guarded dispatch", version: 1},
		{name: "permission error terminates", version: 1, checkError: true},
		{name: "legacy activity sequence", version: workflow.DefaultVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			options := utils.TestWorkerOptions()
			options.Interceptors = append(options.Interceptors, env.NewEnvSwitchGateInterceptor())
			testEnv.SetWorkerOptions(options)
			testEnv.OnGetVersion("run-command-expected-env", workflow.DefaultVersion, 1).Return(tc.version)
			var checkErr error
			if tc.checkError {
				checkErr = temporal.NewNonRetryableApplicationError("permission unavailable", "test", nil)
			}
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{Result: common.PermissionDeny, Message: "local denied"}, checkErr).Maybe()
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				Return(env.EnvRunCommandActivityOutput{}, nil).Maybe()
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				ctx = utils.NoRetryCtx(ctx)
				stale := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/sandbox"}}
				current := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/local"}}
				gs := &flow_action.GlobalState{}
				state := newFlowEnvState(gs)
				state.publish(&stale)
				state.publish(&current)
				ctx = env.WithEnvTracker(ctx, state)
				return RunCommand(DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, EnvContainer: &stale, GlobalState: gs,
				}}, RunCommandParams{Command: "echo hello"})
			})
			if tc.checkError {
				require.ErrorContains(t, testEnv.GetWorkflowError(), "permission unavailable")
			} else {
				require.NoError(t, testEnv.GetWorkflowError())
				var result string
				require.NoError(t, testEnv.GetWorkflowResult(&result))
				if tc.version == workflow.DefaultVersion {
					require.Contains(t, result, "Command executed")
				} else {
					require.Contains(t, result, "local denied")
				}
			}
			if tc.version == workflow.DefaultVersion {
				testEnv.AssertNotCalled(t, "CheckCommandPermissionActivity", mock.Anything, mock.Anything)
				testEnv.AssertNumberOfCalls(t, "EnvRunCommandActivity", 1)
			} else {
				testEnv.AssertNotCalled(t, "EnvRunCommandActivity", mock.Anything, mock.Anything)
				testEnv.AssertNumberOfCalls(t, "CheckCommandPermissionActivity", 1)
			}
			testEnv.AssertExpectations(t)
		})
	}
}

func TestCommandPermissionMetadata(t *testing.T) {
	t.Parallel()
	for _, sandbox := range []bool{false, true} {
		name := "retained approval"
		if sandbox {
			name = "sandbox bypass"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{
					Result:     common.PermissionRequireApproval,
					Evaluation: &common.ScriptPermissionEvaluation{Outcome: common.PermissionRequireApproval},
				}, nil).Maybe()
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (*common.ScriptPermissionEvaluation, error) {
				container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/local"}}
				if sandbox {
					container.Env = &env.ModalEnv{WorkingDirectory: "/sandbox"}
				}
				proceed, _, _, evaluation, err := checkCommandPermissionWithEvaluation(
					DevContext{ExecContext: flow_action.ExecContext{
						Context: utils.NoRetryCtx(ctx), EnvContainer: &container,
					}}, "echo hello", ".", !sandbox)
				if err == nil && !proceed {
					return nil, temporal.NewNonRetryableApplicationError("expected authorization", "test", nil)
				}
				return evaluation, err
			})
			require.NoError(t, testEnv.GetWorkflowError())
			var evaluation common.ScriptPermissionEvaluation
			require.NoError(t, testEnv.GetWorkflowResult(&evaluation))
			if sandbox {
				require.Equal(t, common.PermissionAutoApprove, evaluation.Outcome)
				require.False(t, evaluation.Approved)
				require.Empty(t, evaluation.Commands)
				require.Equal(t, []common.PermissionFactor{{
					Kind: common.PermissionFactorSandboxBypass, Outcome: common.PermissionAutoApprove,
				}}, evaluation.Factors)
				testEnv.AssertNotCalled(t, "CheckCommandPermissionActivity", mock.Anything, mock.Anything)
			} else {
				require.Equal(t, common.PermissionRequireApproval, evaluation.Outcome)
				require.True(t, evaluation.Approved)
			}
		})
	}
}
