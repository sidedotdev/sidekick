package dev

import (
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRunCommandToolCancellation(t *testing.T) {
	t.Parallel()
	for _, signal := range []string{"complete", SignalNamePause, SignalNameUserAction} {
		t.Run(signal, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			storage := sqlite.NewTestSqliteStorage(t, "tool_cancellation")
			activities := &flow_action.FlowActivities{Service: srv.NewDelegator(storage, srv.NewMemoryStreamer())}
			testEnv.RegisterActivity(activities)
			var meta *temporalmeta.TemporalMetaActivities
			testEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
				Return([]domain.TemporalActivityRef{}, nil).Maybe()
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{Result: common.PermissionAutoApprove}, nil)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				After(time.Hour).Return(env.EnvRunCommandActivityOutput{Stdout: "finished"}, nil)

			if signal != "complete" {
				testEnv.RegisterDelayedCallback(func() {
					if signal == SignalNamePause {
						testEnv.SignalWorkflow(signal, Pause{})
					} else {
						testEnv.SignalWorkflow(signal, string(flow_action.UserActionGoNext))
					}
				}, time.Second)
			}

			type result struct {
				Canceled bool
				Elapsed  time.Duration
				Output   ToolCallOutput
			}
			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (result, error) {
				gs := &flow_action.GlobalState{}
				gs.InitValues()
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:     utils.NoRetryCtx(ctx),
					WorkspaceId: "test-workspace",
					GlobalState: gs,
					FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
					EnvContainer: &env.EnvContainer{
						Env: &env.LocalEnv{WorkingDirectory: "."},
					},
				}}
				SetupPauseHandler(dCtx, "", nil)
				SetupUserActionHandler(dCtx)
				start := workflow.Now(ctx)
				future, settable := workflow.NewFuture(ctx)
				workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
					output, err := handleToolCall(dCtx.WithContext(goCtx), llm.ToolCall{
						Id:        "command",
						Name:      runCommandTool.Name,
						Arguments: `{"command":"sleep 3600"}`,
					})
					canceled := temporal.IsCanceledError(err)
					if canceled {
						err = nil
					}
					settable.Set(result{
						Canceled: canceled,
						Elapsed:  workflow.Now(goCtx).Sub(start),
						Output:   output,
					}, err)
				})
				var output result
				err := future.Get(ctx, &output)
				return output, err
			})

			require.NoError(t, testEnv.GetWorkflowError())
			var output result
			require.NoError(t, testEnv.GetWorkflowResult(&output))
			if signal == "complete" {
				require.False(t, output.Canceled)
				require.GreaterOrEqual(t, output.Elapsed, time.Hour)
				require.Contains(t, contentText(output.Output.Content), "finished")
			} else {
				require.True(t, output.Canceled)
				require.Less(t, output.Elapsed, time.Minute)
			}
			testEnv.AssertExpectations(t)
		})
	}
}
