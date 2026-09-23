package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestGenerationNoToolResponsesRequestHelp(t *testing.T) {
	t.Parallel()
	for _, planning := range []bool{false, true} {
		t.Run(fmt.Sprintf("planning=%v", planning), func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
			env.OnGetVersion("pause-flow", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
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

			texts := []string{"record", "  Which behavior?\n", "Another question", "", " \n", "Please clarify", "", "record", "", " ", "\n", "", "", "", "..."}
			helpAt := map[int]bool{1: true, 2: true, 5: true, 10: true, 13: true, 14: true}
			requestCount := 0
			call := 0
			var activities *persisted_ai.Llm2Activities
			env.OnActivity(activities.Stream, mock.Anything, mock.Anything).
				Return(func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
					require.Less(t, call, len(texts))
					if call > 0 && helpAt[call-1] {
						require.Contains(t, messages[len(messages)-1].GetContentString(), "User answer")
					}
					text := texts[call]
					message := llm2.Message{Role: llm2.RoleAssistant, Content: llm2.TextContentBlocks(text)}
					if text == "record" {
						name := recordDevRequirementsTool.Name
						args := `{"overview":"Draft","acceptance_criteria":["Fix behavior"],"requirements_finalized":false}`
						if planning {
							name = recordDevPlanTool.Name
							args = `{"steps":[{"step_number":"1","title":"Draft","definition":"Fix behavior","type":"edit"}],"is_planning_complete":false}`
						}
						message.Content = []llm2.ContentBlock{{
							Type: llm2.ContentBlockTypeToolUse,
							ToolUse: &llm2.ToolUseBlock{
								Id: fmt.Sprintf("record-%d", call), Name: name, Arguments: args,
							},
						}}
					}
					call++
					return &llm2.MessageResponse{StopReason: "stop", Output: message}, nil
				}).Maybe()

			child := func(ctx workflow.Context) error {
				dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
				dCtx.Idd = true
				if err := SetupModelConfigHandlers(dCtx); err != nil {
					return err
				}
				reqState := &buildDevRequirementsState{advisor: newAdvisor(dCtx, false, common.PlanningKey)}
				planState := &buildDevPlanState{advisor: newAdvisor(dCtx, false, common.PlanningKey)}
				iteration := &LlmIteration{ExecCtx: dCtx, ChatHistory: NewVersionedChatHistory(ctx, dCtx.WorkspaceId)}
				for i, text := range texts {
					iteration.Num = i
					iteration.AutoIterationCount = 7
					start := len(messages)
					if planning {
						iteration.State = planState
						result, err := buildDevPlanIteration(iteration)
						if err != nil {
							return err
						}
						require.Nil(t, result)
						require.Len(t, planState.devPlan.Steps, 1)
						require.False(t, planState.devPlan.Complete)
					} else {
						iteration.State = reqState
						result, err := buildDevRequirementsIteration(iteration)
						if err != nil {
							return err
						}
						require.Nil(t, result)
						require.Equal(t, "Draft", reqState.devRequirements.Overview)
						require.False(t, reqState.devRequirements.Complete)
					}
					added := messages[start:]
					require.Len(t, added, 2)
					calls := added[0].GetToolCalls()
					if helpAt[i] {
						require.Len(t, calls, 1)
						require.Equal(t, getHelpOrInputTool.Name, calls[0].Name)
						var args GetHelpOrInputArguments
						require.NoError(t, json.Unmarshal([]byte(calls[0].Arguments), &args))
						require.Len(t, args.Requests, 1)
						if strings.TrimSpace(text) != "" {
							require.Equal(t, text, args.Requests[0].Content)
						} else {
							require.Contains(t, args.Requests[0].Content, "empty responses")
						}
						require.NotEmpty(t, args.Requests[0].SelfHelp.Analysis)
						require.Equal(t, []string{}, args.Requests[0].SelfHelp.Tools)
						require.Equal(t, []string{}, args.Requests[0].SelfHelp.AlreadyAttemptedTools)
						require.Equal(t, 0, iteration.AutoIterationCount)
						require.Contains(t, added[1].GetContentString(), "User answer")
						require.Equal(t, calls[0].Id, added[1].Content[0].ToolResult.ToolCallId)
					} else if text == "record" {
						require.Len(t, calls, 1)
						require.Equal(t, fmt.Sprintf("record-%d", i), calls[0].Id)
					} else {
						require.Empty(t, calls)
						require.Contains(t, added[1].GetContentString(), "record")
					}
				}
				return nil
			}
			env.RegisterWorkflow(child)
			parent := func(ctx workflow.Context) error {
				requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						requests.Receive(ctx, &req)
						requestCount++
						require.NotEmpty(t, strings.TrimSpace(req.Content))
						require.NoError(t, workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
							flow_action.UserResponseSignalName(req.FlowActionId),
							flow_action.UserResponse{FlowActionId: req.FlowActionId, Content: "User answer"}).Get(ctx, nil))
					}
				})
				return workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, nil)
			}
			env.ExecuteWorkflow(parent)
			require.NoError(t, env.GetWorkflowError())
			require.Equal(t, len(texts), call)
			require.Equal(t, len(helpAt), requestCount)
		})
	}
}
