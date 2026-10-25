package dev

import (
	"context"
	"testing"

	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestAdvisorDeliverableBatchOrdering(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "advisor-deliverables")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.OnGetVersion("deliverable-history", workflow.DefaultVersion, 1).Return(workflow.Version(1))
	histories := &persisted_ai.ChatHistoryActivities{Storage: storage}
	env.RegisterActivity(histories.AppendMessage)
	var actions *flow_action.FlowActivities
	env.OnActivity(actions.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	var meta *temporalmeta.TemporalMetaActivities
	env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	wrapper := func(ctx workflow.Context) (*persisted_ai.ChatHistoryContainer, error) {
		dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherType(""))
		executorHistory := NewVersionedChatHistory(ctx, dCtx.WorkspaceId)
		advisorHistory := NewVersionedChatHistory(ctx, dCtx.WorkspaceId)
		iteration := &LlmIteration{ExecCtx: dCtx, ChatHistory: executorHistory}
		var requirements *DevRequirements
		var plan *DevPlan
		handlers := devRequirementsToolHandlers(iteration, &buildDevRequirementsState{}, &requirements)
		for name, handler := range devPlanToolHandlers(iteration, &buildDevPlanState{}, &plan) {
			handlers[name] = handler
		}
		calls := []llm.ToolCall{
			{Id: "req-record", Name: recordDevRequirementsTool.Name, Arguments: `{"overview":"original","requirements_finalized":false}`},
			{Id: "plan-record", Name: recordDevPlanTool.Name, Arguments: `{"steps":[{"step_number":"1","title":"original","definition":"implementation","type":"edit"}],"is_planning_complete":false}`},
			{Id: "req-update", Name: updateDevRequirementsTool.Name, Arguments: `{"overview":"updated"}`},
			{Id: "plan-update", Name: updateDevPlanTool.Name, Arguments: `{"updates":[{"step_number":"1","operation":"edit","title":"updated"}]}`},
		}
		injected, err := applyAdvisorToolCalls(dCtx.ExecContext, executorHistory, advisorHistory, "guidance", calls)
		if err != nil {
			return nil, err
		}
		_, err = handleToolCallsWithOptions(dCtx, injected, executorHistory, handlers, toolCallOptions{
			AssistantMessage: llm.ChatMessage{
				Role: llm.ChatMessageRoleAssistant, Content: executorPuppetContent("guidance"), ToolCalls: injected,
			},
		})
		return executorHistory, err
	}
	env.ExecuteWorkflow(wrapper)
	require.NoError(t, env.GetWorkflowError())
	var history persisted_ai.ChatHistoryContainer
	require.NoError(t, env.GetWorkflowResult(&history))
	require.NoError(t, history.History.Hydrate(context.Background(), storage))
	messages := history.History.(*persisted_ai.Llm2ChatHistory).Llm2Messages()
	require.Len(t, messages, 8)
	for i, message := range messages {
		block := message.Content[len(message.Content)-1]
		meta := persisted_ai.GetDeliverable(block)
		require.NotNil(t, meta)
		id := "dev_requirements"
		if i%4 >= 2 {
			id = "dev_plan"
		}
		require.Equal(t, id, meta.ID)
		if i%2 == 0 {
			operation := "record"
			if i >= 4 {
				operation = "update"
			}
			require.Equal(t, operation, meta.Operation)
			require.Empty(t, meta.Outcome)
		} else {
			require.Equal(t, "success", meta.Outcome)
			require.Empty(t, meta.Operation)
			require.Equal(t, i >= 4, meta.Snapshot)
		}
	}
}
