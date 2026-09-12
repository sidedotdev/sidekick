package dev

import (
	"context"
	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestBuildDevPlanRecordsBeforeFinishingAtLimit(t *testing.T) {
	t.Parallel()

	for _, partial := range []bool{false, true} {
		name := "unrecorded"
		if partial {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
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
					return &persisted_ai.MessageRef{BlockKeys: []string{"message"}, Role: string(input.Message.Role)}, nil
				}).Maybe()
			env.OnActivity(histories.ManageV4, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
					return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
				}).Maybe()

			calls := 0
			forced := false
			var activities *persisted_ai.Llm2Activities
			env.OnActivity(activities.Stream, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
					calls++
					require.LessOrEqual(t, calls, 12, "planner must switch to forced recording")
					if input.Options.ToolChoice.Type == common.ToolChoiceTypeTool ||
						input.Options.ToolChoice.Type == common.ToolChoiceTypeRequired {
						forced = true
						expectedTool := recordDevPlanTool.Name
						args := `{"steps":[{"step_number":"1","title":"Fix behavior","definition":"Implement the fix","type":"edit"}],"is_planning_complete":true}`
						if partial {
							expectedTool = updateDevPlanTool.Name
							args = `{"is_planning_complete":true}`
						}
						require.Len(t, input.Options.Tools, 1)
						require.Equal(t, expectedTool, input.Options.Tools[0].Name)
						return &llm2.MessageResponse{
							StopReason: "tool_use",
							Output: llm2.Message{
								Role: llm2.RoleAssistant,
								Content: []llm2.ContentBlock{{
									Type: llm2.ContentBlockTypeToolUse,
									ToolUse: &llm2.ToolUseBlock{
										Id: "finish-plan", Name: expectedTool, Arguments: args,
									},
								}},
							},
						}, nil
					}
					return &llm2.MessageResponse{
						StopReason: "stop",
						Output: llm2.Message{
							Role:    llm2.RoleAssistant,
							Content: llm2.TextContentBlocks("Still considering the plan."),
						},
					}, nil
				}).Maybe()

			wrapper := func(ctx workflow.Context) (*DevPlan, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
				dCtx.Idd = true
				if err := SetupModelConfigHandlers(dCtx); err != nil {
					return nil, err
				}
				state := &buildDevPlanState{}
				state.advisor = newAdvisor(dCtx, false, common.PlanningKey)
				if partial {
					state.devPlan = DevPlan{Steps: []DevStep{{
						StepNumber: "1", Title: "Fix behavior", Definition: "Implement the fix", Type: "edit",
					}}}
				}
				return LlmLoop(dCtx, NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
					buildDevPlanIteration, WithInitialState(state), WithFeedbackEvery(9), WithMaxIterations(10))
			}
			env.ExecuteWorkflow(wrapper)
			require.NoError(t, env.GetWorkflowError())
			var plan DevPlan
			require.NoError(t, env.GetWorkflowResult(&plan))
			require.True(t, forced, "planning must not silently return an empty result")
			require.True(t, plan.Complete)
			require.Len(t, plan.Steps, 1)
			require.Greater(t, calls, 1)
			require.NotEmpty(t, messages)
		})
	}
}
