package dev

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/temporalmeta"
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
			env.OnGetVersion("dev-plan-no-tool-help", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

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

func TestBuildDevPlanFeedbackRestoresTools(t *testing.T) {
	t.Parallel()

	for _, feedback := range []string{"review", "pause", "resume", "help"} {
		paused := feedback == "pause" || feedback == "resume"
		for _, partial := range []bool{false, true} {
			name := feedback + "/unrecorded"
			if partial {
				name = feedback + "/partial"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var suite testsuite.WorkflowTestSuite
				env := suite.NewTestWorkflowEnvironment()
				env.SetWorkerOptions(utils.TestWorkerOptions())
				env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
				env.OnGetVersion("dev-plan-no-tool-help", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
				env.OnGetVersion("pause-flow", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

				var metadata *temporalmeta.TemporalMetaActivities
				env.OnActivity(metadata.FetchFlowActionActivities, mock.Anything, mock.Anything).
					Return([]domain.TemporalActivityRef{}, nil).Maybe()
				var flags *fflag.FFlagActivities
				env.OnActivity(flags.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()
				var flows *flow_action.FlowActivities
				env.OnActivity(flows.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
				env.OnActivity(flows.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
					Return(common.ModelMetadata{}, nil).Maybe()

				var histories *persisted_ai.ChatHistoryActivities
				env.OnActivity(histories.AppendMessage, mock.Anything, mock.Anything).
					Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
						return &persisted_ai.MessageRef{BlockKeys: []string{"message"}, Role: string(input.Message.Role)}, nil
					}).Maybe()
				env.OnActivity(histories.ManageV4, mock.Anything, mock.Anything).
					Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
						return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
					}).Maybe()

				feedbackReceived := false
				calls := 0
				resumedCalls := 0
				resumedLimit := 8
				if paused {
					resumedLimit++
				}
				var activities *persisted_ai.Llm2Activities
				env.OnActivity(activities.Stream, mock.Anything, mock.Anything).
					Return(func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
						calls++
						if feedback == "help" && calls == 7 {
							return &llm2.MessageResponse{
								StopReason: "tool_use",
								Output: llm2.Message{
									Role: llm2.RoleAssistant,
									Content: []llm2.ContentBlock{{
										Type: llm2.ContentBlockTypeToolUse,
										ToolUse: &llm2.ToolUseBlock{
											Id:        "ask-user",
											Name:      getHelpOrInputTool.Name,
											Arguments: `{"requests":[{"content":"Which edge cases should the plan address?","selfHelp":{"analysis":"Only the user can clarify the intended behavior.","tools":[],"alreadyAttemptedTools":[]}}]}`,
										},
									}},
								},
							}, nil
						}
						forced := input.Options.ToolChoice.Type == common.ToolChoiceTypeTool ||
							input.Options.ToolChoice.Type == common.ToolChoiceTypeRequired
						if feedbackReceived {
							resumedCalls++
							require.LessOrEqual(t, resumedCalls, resumedLimit)
							if resumedCalls < resumedLimit {
								require.False(t, forced, "feedback must reopen planning before forcing another plan update")
								var toolNames []string
								for _, tool := range input.Options.Tools {
									toolNames = append(toolNames, tool.Name)
								}
								require.Contains(t, toolNames, getHelpOrInputTool.Name)
							} else {
								require.True(t, forced, "planning limits must still apply after feedback")
							}
						}
						if forced {
							tool := recordDevPlanTool.Name
							args := `{"steps":[{"step_number":"1","title":"Fix behavior","definition":"Implement the fix","type":"edit"}],"is_planning_complete":true}`
							if partial || feedbackReceived && feedback == "review" {
								tool = updateDevPlanTool.Name
								args = `{"is_planning_complete":true}`
							}
							return &llm2.MessageResponse{
								StopReason: "tool_use",
								Output: llm2.Message{
									Role: llm2.RoleAssistant,
									Content: []llm2.ContentBlock{{
										Type: llm2.ContentBlockTypeToolUse,
										ToolUse: &llm2.ToolUseBlock{
											Id: "finish-plan", Name: tool, Arguments: args,
										},
									}},
								},
							}, nil
						}
						return &llm2.MessageResponse{
							StopReason: "stop",
							Output: llm2.Message{
								Role:    llm2.RoleAssistant,
								Content: llm2.TextContentBlocks("Investigating the requested behavior."),
							},
						}, nil
					}).Maybe()

				child := func(ctx workflow.Context) (*DevPlan, error) {
					dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
					if err := SetupModelConfigHandlers(dCtx); err != nil {
						return nil, err
					}
					state := &buildDevPlanState{advisor: newAdvisor(dCtx, false, common.PlanningKey)}
					if partial {
						state.devPlan = DevPlan{Steps: []DevStep{{
							StepNumber: "1", Title: "Fix behavior", Definition: "Implement the fix", Type: "edit",
						}}}
					}
					return LlmLoop(dCtx, NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
						func(iteration *LlmIteration) (*DevPlan, error) {
							plan, err := buildDevPlanIteration(iteration)
							if paused && iteration.Num == 7 {
								dCtx.GlobalState.Paused = true
							}
							return plan, err
						}, WithInitialState(state), WithFeedbackEvery(8), WithMaxIterations(9))
				}
				env.RegisterWorkflow(child)
				parent := func(ctx workflow.Context) (*DevPlan, error) {
					workflow.Go(ctx, func(ctx workflow.Context) {
						requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
						for {
							var req flow_action.RequestForUser
							requests.Receive(ctx, &req)
							approved := feedbackReceived
							content := "Please investigate the edge cases before finalizing."
							if feedbackReceived {
								content = "Approved."
							} else if feedback == "resume" {
								content = ""
							}
							feedbackReceived = true
							err := workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
								flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
									FlowActionId: req.FlowActionId,
									Approved:     &approved,
									Content:      content,
								}).Get(ctx, nil)
							require.NoError(t, err)
						}
					})
					var plan DevPlan
					err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &plan)
					return &plan, err
				}
				env.ExecuteWorkflow(parent)
				require.NoError(t, env.GetWorkflowError())
				var plan DevPlan
				require.NoError(t, env.GetWorkflowResult(&plan))
				require.True(t, plan.Complete)
				require.Len(t, plan.Steps, 1)
				require.Equal(t, resumedLimit, resumedCalls)
			})
		}
	}
}

func TestBuildDevPlanFeedbackResetVersion(t *testing.T) {
	t.Parallel()

	for _, legacy := range []bool{false, true} {
		name := "current"
		version := workflow.Version(1)
		if legacy {
			name = "legacy"
			version = workflow.DefaultVersion
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.OnGetVersion("llm-loop-reset-after-feedback", workflow.DefaultVersion, 1).Return(version)
			var histories *persisted_ai.ChatHistoryActivities
			env.OnActivity(histories.AppendMessage, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
					return &persisted_ai.MessageRef{BlockKeys: []string{"message"}, Role: string(input.Message.Role)}, nil
				}).Maybe()

			wrapper := func(ctx workflow.Context) error {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
				state := &buildDevPlanState{forceRecording: true}
				iteration := &LlmIteration{
					LlmLoopConfig: LlmLoopConfig{autoIterations: 8, maxIterations: 9},
					Num:           10, AutoIterationCount: 8,
					ExecCtx: dCtx, ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId),
					State: state,
				}
				iteration.resetAfterFeedback()
				require.Zero(t, iteration.AutoIterationCount)
				require.Equal(t, 10, iteration.Num)

				for range 2 {
					finalizing, err := state.prepareFinalization(iteration)
					require.NoError(t, err)
					require.Equal(t, legacy, finalizing)
					iteration.Num++
					iteration.AutoIterationCount++
				}
				return nil
			}
			env.ExecuteWorkflow(wrapper)
			require.NoError(t, env.GetWorkflowError())
		})
	}
}
