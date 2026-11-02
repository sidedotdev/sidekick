package dev

import (
	"context"
	"encoding/json"
	"sync"
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRunCommandApprovalMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		approve    bool
		direct     bool
		missingID  bool
		headless   bool
		oldVersion bool
	}{
		{name: "approved identical invocations", approve: true},
		{name: "rejected identical invocations"},
		{name: "direct caller", direct: true},
		{name: "missing tool call ID", missingID: true},
		{name: "headless", headless: true},
		{name: "legacy version", oldVersion: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			storage := sqlite.NewTestSqliteStorage(t, "approval_metadata")
			t.Cleanup(func() { require.NoError(t, storage.Close()) })
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			if tc.oldVersion {
				testEnv.OnGetVersion("run-command-approval-metadata", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion)
			}
			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{Result: common.PermissionRequireApproval}, nil)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				Return(env.EnvRunCommandActivityOutput{Stdout: "ok"}, nil).Maybe()
			var meta *temporalmeta.TemporalMetaActivities
			testEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
				Return([]domain.TemporalActivityRef{{ActivityType: "test", ActivityId: "1", ScheduledEventId: 1}}, nil)
			var fa *flow_action.FlowActivities
			var mu sync.Mutex
			var persisted []domain.FlowAction
			testEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(
				func(ctx context.Context, action domain.FlowAction) error {
					if err := storage.PersistFlowAction(ctx, action); err != nil {
						return err
					}
					mu.Lock()
					persisted = append(persisted, action)
					mu.Unlock()
					return nil
				},
			)
			var activities srv.Activities
			testEnv.OnActivity(activities.GetFlow, mock.Anything, mock.Anything, mock.Anything).
				Return(domain.Flow{}, nil).Maybe()
			testEnv.OnActivity(activities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

			child := func(ctx workflow.Context) error {
				ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
				state := &flow_action.GlobalState{}
				state.InitValues()
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: ctx, WorkspaceId: "ws_metadata", GlobalState: state,
					FlowScope:             &flow_action.FlowScope{SubflowName: "commands"},
					EnvContainer:          &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "."}},
					DisableHumanInTheLoop: tc.headless,
				}}
				for _, id := range []string{"call_one", "call_two"} {
					if tc.direct {
						if _, err := RunCommand(dCtx, RunCommandParams{Command: "echo identical"}); err != nil {
							return err
						}
					} else {
						if tc.missingID {
							id = ""
						}
						if _, err := handleToolCall(dCtx, llm.ToolCall{
							Id: id, Name: "run_command", Arguments: `{"command":"echo identical"}`,
						}); err != nil {
							return err
						}
					}
				}
				return workflow.Sleep(ctx, time.Second)
			}
			var pending []domain.FlowAction
			parent := func(ctx workflow.Context) error {
				signals := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						signals.Receive(ctx, &req)
						action, err := storage.GetFlowAction(context.Background(), "ws_metadata", req.FlowActionId)
						assert.NoError(t, err)
						assert.Equal(t, domain.ActionStatusPending, action.ActionStatus)
						pending = append(pending, action)
						approved := tc.approve
						workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
							flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
								FlowActionId: req.FlowActionId, Approved: &approved,
							}).Get(ctx, nil)
					}
				})
				return workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, nil)
			}
			testEnv.RegisterWorkflow(child)
			testEnv.RegisterWorkflow(parent)
			testEnv.ExecuteWorkflow(parent)
			require.NoError(t, testEnv.GetWorkflowError())

			mu.Lock()
			snapshots := append([]domain.FlowAction(nil), persisted...)
			mu.Unlock()
			var origins []string
			approvalIDs := make(map[string]int)
			for _, action := range snapshots {
				if action.ActionType == "tool_call.run_command" && action.ActionStatus == domain.ActionStatusStarted {
					origins = append(origins, action.Id)
				}
				if action.ActionType == "user_request.approve.run_command" {
					if _, ok := approvalIDs[action.Id]; !ok {
						approvalIDs[action.Id] = len(approvalIDs)
					}
				}
			}
			require.Len(t, approvalIDs, 2)
			if !tc.direct {
				require.Len(t, origins, 2)
				assert.NotEqual(t, origins[0], origins[1])
			}
			if tc.headless {
				require.Empty(t, pending)
			} else {
				require.Len(t, pending, 2)
			}
			for _, action := range append(snapshots, pending...) {
				if action.ActionType != "user_request.approve.run_command" {
					continue
				}
				data, err := json.Marshal(action)
				require.NoError(t, err)
				var decoded map[string]any
				require.NoError(t, json.Unmarshal(data, &decoded))
				if tc.direct || tc.oldVersion {
					assert.NotContains(t, decoded, "metadata")
					continue
				}
				index := approvalIDs[action.Id]
				link := map[string]any{"rel": "approval_for", "flowActionId": origins[index]}
				if !tc.missingID {
					link["toolCallId"] = []string{"call_one", "call_two"}[index]
				}
				assert.Equal(t, map[string]any{"links": []any{link}}, decoded["metadata"])
				assert.NotEqual(t, origins[index], action.Id)
			}
			for id := range approvalIDs {
				final, err := storage.GetFlowAction(context.Background(), "ws_metadata", id)
				require.NoError(t, err)
				assert.Equal(t, domain.ActionStatusComplete, final.ActionStatus)
				require.Len(t, final.TemporalActivities, 1)
				var response flow_action.UserResponse
				require.NoError(t, json.Unmarshal([]byte(final.ActionResult), &response))
				require.NotNil(t, response.Approved)
				assert.Equal(t, tc.approve || tc.headless, *response.Approved)
			}
			testEnv.AssertExpectations(t)
		})
	}
}
