package dev

import (
	"encoding/json"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDeliverableResultsAndFeedback(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"dev_requirements", "dev_plan"} {
		for _, operation := range []string{"record", "update"} {
			for _, fail := range []bool{false, true} {
				name := id + "/" + operation + "/rejected"
				if fail {
					name = id + "/" + operation + "/failed"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					var suite testsuite.WorkflowTestSuite
					env := suite.NewTestWorkflowEnvironment()
					env.SetWorkerOptions(utils.TestWorkerOptions())
					env.OnGetVersion("deliverable-history", workflow.DefaultVersion, 1).Return(workflow.Version(1))
					env.OnGetVersion("pause-flow", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
					var actions *flow_action.FlowActivities
					env.OnActivity(actions.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
					var meta *temporalmeta.TemporalMetaActivities
					env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
						Return([]domain.TemporalActivityRef{}, nil).Maybe()

					const feedback = "Please preserve this specific human feedback."
					child := func(ctx workflow.Context) (*llm2.Message, error) {
						dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
						iteration := &LlmIteration{ExecCtx: dCtx}
						var handlers map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error)
						var toolName, args string
						if id == "dev_requirements" {
							state := &buildDevRequirementsState{devRequirements: DevRequirements{
								Overview: "artifact overview", AcceptanceCriteria: []string{"artifact criterion"},
								Learnings: []string{"artifact learning"},
							}}
							var recorded *DevRequirements
							handlers = devRequirementsToolHandlers(iteration, state, &recorded)
							toolName = recordDevRequirementsTool.Name
							args = `{"overview":"artifact overview","acceptance_criteria":["artifact criterion"],"learnings":["artifact learning"],"requirements_finalized":true}`
							if operation == "update" {
								toolName = updateDevRequirementsTool.Name
								args = `{"requirements_finalized":true}`
							}
						} else {
							state := &buildDevPlanState{devPlan: DevPlan{Steps: []DevStep{{
								StepNumber: "1", Title: "artifact title", Definition: "artifact definition", Type: "edit",
							}}}}
							var recorded *DevPlan
							handlers = devPlanToolHandlers(iteration, state, &recorded)
							toolName = recordDevPlanTool.Name
							args = `{"steps":[{"step_number":"1","title":"artifact title","definition":"artifact definition","type":"edit"}],"is_planning_complete":true}`
							if operation == "update" {
								toolName = updateDevPlanTool.Name
								args = `{"is_planning_complete":true}`
							}
						}
						if fail {
							args = "[]"
						}
						result, err := handlers[toolName](dCtx, llm.ToolCall{Id: "attempt", Name: toolName, Arguments: args})
						if err != nil {
							return nil, err
						}
						return toolResultMessage(result), nil
					}
					env.RegisterWorkflow(child)
					parent := func(ctx workflow.Context) (*llm2.Message, error) {
						requests := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
						workflow.Go(ctx, func(ctx workflow.Context) {
							for {
								var request flow_action.RequestForUser
								requests.Receive(ctx, &request)
								approved := false
								err := workflow.SignalExternalWorkflow(ctx, request.OriginWorkflowId, "",
									flow_action.UserResponseSignalName(request.FlowActionId), flow_action.UserResponse{
										FlowActionId: request.FlowActionId, Approved: &approved, Content: feedback,
									}).Get(ctx, nil)
								require.NoError(t, err)
							}
						})
						var result llm2.Message
						err := workflow.ExecuteChildWorkflow(ctx, child).Get(ctx, &result)
						return &result, err
					}
					env.ExecuteWorkflow(parent)
					require.NoError(t, env.GetWorkflowError())
					var message llm2.Message
					require.NoError(t, env.GetWorkflowResult(&message))
					descriptor := persisted_ai.GetDeliverable(message.Content[0])
					require.NotNil(t, descriptor)
					require.Equal(t, id, descriptor.ID)
					require.Empty(t, descriptor.Operation)
					result := message.Content[0].ToolResult
					require.NotNil(t, result)
					if fail {
						require.Equal(t, "failure", descriptor.Outcome)
						require.False(t, descriptor.Snapshot)
						require.True(t, result.IsError)
						require.NotEmpty(t, result.TextContent())
						require.Len(t, message.Content, 1)
						return
					}
					require.Equal(t, "success", descriptor.Outcome)
					require.Equal(t, operation == "update", descriptor.Snapshot)
					require.False(t, result.IsError)
					require.NotContains(t, result.TextContent(), feedback)
					require.Len(t, message.Content, 2)
					require.Equal(t, feedback, message.Content[1].Text)
					require.Equal(t, persisted_ai.ContextTypeUserFeedback, persisted_ai.GetContextType(message.Content[1]))
					if operation == "record" {
						require.NotContains(t, result.TextContent(), "artifact")
					} else {
						require.True(t, json.Valid([]byte(result.Content[len(result.Content)-1].Text)))
						require.Contains(t, result.TextContent(), "artifact")
					}

					request := llm2.ContentBlock{Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "attempt", Name: result.Name}}
					persisted_ai.SetDeliverable(&request, persisted_ai.DeliverableMetadata{ID: id, Operation: operation})
					newRequest := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "new operation"}
					persisted_ai.SetDeliverable(&newRequest, persisted_ai.DeliverableMetadata{ID: id, Operation: "update"})
					newReport := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "new snapshot"}
					persisted_ai.SetDeliverable(&newReport, persisted_ai.DeliverableMetadata{ID: id, Outcome: "success", Snapshot: true})
					managed, err := persisted_ai.ManageLlm2ChatHistoryWithPolicy([]llm2.Message{
						{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{request}},
						message,
						{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{newRequest}},
						{Role: llm2.RoleUser, Content: []llm2.ContentBlock{newReport}},
						{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("continue")},
					}, persisted_ai.ManagePolicy{KeepLength: 1, TruncationTrigger: 1}, common.ModelConfig{})
					require.NoError(t, err)
					found := false
					for _, msg := range managed {
						for _, block := range msg.Content {
							if block.Text == feedback {
								found = true
							}
						}
					}
					require.True(t, found, "human feedback must survive superseding the artifact")
				})
			}
		}
	}
}
