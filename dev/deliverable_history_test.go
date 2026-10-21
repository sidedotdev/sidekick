package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestBuildDeliverableHistoryMultipleOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id       string
		record   string
		update   string
		args     string
		patch    string
		snapshot string
	}{
		{
			"dev_requirements", recordDevRequirementsTool.Name, updateDevRequirementsTool.Name,
			`{"overview":"original requirements","acceptance_criteria":["criterion"],"requirements_finalized":false}`,
			`{"overview":"updated requirements"}`, "updated requirements",
		},
		{
			"dev_plan", recordDevPlanTool.Name, updateDevPlanTool.Name,
			`{"steps":[{"step_number":"1","title":"original plan","definition":"Implement the fix","type":"edit"}],"is_planning_complete":false}`,
			`{"updates":[{"step_number":"1","operation":"edit","title":"updated plan"}]}`, "updated plan",
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
			var flags *fflag.FFlagActivities
			env.OnActivity(flags.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()
			var flows *flow_action.FlowActivities
			env.OnActivity(flows.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			env.OnActivity(flows.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
				Return(common.ModelMetadata{}, nil).Maybe()

			var messages []llm2.Message
			var histories *persisted_ai.ChatHistoryActivities
			env.OnActivity(histories.AppendMessage, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
					messages = append(messages, input.Message)
					return &persisted_ai.MessageRef{
						BlockKeys: []string{fmt.Sprint(len(messages))}, Role: string(input.Message.Role),
					}, nil
				}).Maybe()
			env.OnActivity(histories.ManageV4, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
					return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
				}).Maybe()

			var activities *persisted_ai.Llm2Activities
			env.OnActivity(activities.Stream, mock.Anything, mock.Anything).
				Return(&llm2.MessageResponse{
					StopReason: "tool_use",
					Output: llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{
						{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "record", Name: tc.record, Arguments: tc.args}},
						{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "update", Name: tc.update, Arguments: tc.patch}},
					}},
				}, nil).Once()

			wrapper := func(ctx workflow.Context) error {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
				if err := SetupModelConfigHandlers(dCtx); err != nil {
					return err
				}
				iteration := &LlmIteration{ExecCtx: dCtx, ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId)}
				if tc.id == "dev_requirements" {
					iteration.State = &buildDevRequirementsState{advisor: newAdvisor(dCtx, false, common.PlanningKey)}
					_, err := buildDevRequirementsIteration(iteration)
					return err
				}
				iteration.State = &buildDevPlanState{advisor: newAdvisor(dCtx, false, common.PlanningKey)}
				_, err := buildDevPlanIteration(iteration)
				return err
			}
			env.ExecuteWorkflow(wrapper)
			require.NoError(t, env.GetWorkflowError())
			require.Len(t, messages, 4, "each operation must precede its own outcome, without overlapping the next operation")
			for i, msg := range messages {
				require.Len(t, msg.Content, 1)
				meta := persisted_ai.GetDeliverable(msg.Content[0])
				require.NotNil(t, meta)
				require.Equal(t, tc.id, meta.ID)
				switch i {
				case 0:
					require.Equal(t, "record", meta.Operation)
					require.Empty(t, meta.Outcome)
					require.True(t, meta.Snapshot)
				case 1:
					require.Equal(t, "success", meta.Outcome)
					require.Empty(t, meta.Operation)
					require.False(t, meta.Snapshot)
					require.NotContains(t, msg.Content[0].ToolResult.TextContent(), "original")
				case 2:
					require.Equal(t, "update", meta.Operation)
					require.Empty(t, meta.Outcome)
					require.False(t, meta.Snapshot)
				case 3:
					require.Equal(t, "success", meta.Outcome)
					require.Empty(t, meta.Operation)
					require.True(t, meta.Snapshot)
					require.Contains(t, msg.Content[0].ToolResult.TextContent(), tc.snapshot)
					require.NotContains(t, msg.Content[0].ToolResult.TextContent(), "original")
					content := msg.Content[0].ToolResult.Content
					if tc.id == "dev_requirements" {
						var snapshot DevRequirements
						require.NoError(t, json.Unmarshal([]byte(content[len(content)-1].Text), &snapshot))
						require.Equal(t, tc.snapshot, snapshot.Overview)
						require.Equal(t, []string{"criterion"}, snapshot.AcceptanceCriteria)
						require.False(t, snapshot.Complete)
					} else {
						var snapshot DevPlan
						require.NoError(t, json.Unmarshal([]byte(content[len(content)-1].Text), &snapshot))
						require.Len(t, snapshot.Steps, 1)
						require.Equal(t, tc.snapshot, snapshot.Steps[0].Title)
						require.Equal(t, "edit", snapshot.Steps[0].Type)
						require.Equal(t, "Implement the fix", snapshot.Steps[0].Definition)
						require.False(t, snapshot.Complete)
					}
				}
			}
		})
	}
}
