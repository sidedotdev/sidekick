package dev

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRenderContextGatherPrompt(t *testing.T) {
	t.Parallel()

	t.Run("basic includes complete requirements", func(t *testing.T) {
		t.Parallel()

		prompt, err := renderContextGatherPrompt(InitialCodeInfo{
			Requirements: "Complete basic requirements marker",
		}, false)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Complete basic requirements marker")
		assert.Contains(t, prompt, "all repository context needed")
		assert.Contains(t, prompt, contextGatherReadyTool.Name)
		assert.Contains(t, prompt, getHelpOrInputTool.Name)
		assert.NotContains(t, prompt, contextGatherForgetTool.Name)
	})

	t.Run("planned preserves plan and step context", func(t *testing.T) {
		t.Parallel()

		plan := &DevPlan{
			Learnings: []string{"Plan learning marker"},
		}
		step := DevStep{
			StepNumber:         "4",
			Title:              "Current step title",
			Type:               "edit",
			Definition:         "Current step definition marker",
			CompletionAnalysis: "Current completion marker",
		}
		prompt, err := renderContextGatherPrompt(InitialDevStepInfo{
			Requirements:  "Complete planned requirements marker",
			PlanExecution: DevPlanExecution{Plan: plan},
			Step:          step,
		}, true)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Complete planned requirements marker")
		assert.Contains(t, prompt, "Plan learning marker")
		assert.Contains(t, prompt, "Current step title")
		assert.Contains(t, prompt, "Current step definition marker")
		assert.Contains(t, prompt, "Current completion marker")
		assert.Contains(t, prompt, contextGatherForgetTool.Name)
		assert.Contains(t, prompt, contextGatherRememberTool.Name)
	})
}

func TestContextGatherToolsAreReadOnly(t *testing.T) {
	t.Parallel()

	toolNames := func(tools []*llm.Tool) []string {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		return names
	}

	disabled := toolNames(contextGatherTools(common.ModelConfig{Provider: "test"}, false))
	assert.ElementsMatch(t, []string{
		bulkSearchRepositoryTool.Name,
		currentGetSymbolDefinitionsTool().Name,
		bulkReadFileTool.Name,
		contextGatherReadyTool.Name,
		getHelpOrInputTool.Name,
	}, disabled)
	assert.NotContains(t, disabled, runCommandTool.Name)
	assert.NotContains(t, disabled, setBaseBranchTool.Name)
	assert.NotContains(t, disabled, doneTool.Name)
	assert.NotContains(t, disabled, contextGatherForgetTool.Name)
	assert.NotContains(t, disabled, contextGatherRememberTool.Name)

	enabled := toolNames(contextGatherTools(common.ModelConfig{Provider: "anthropic"}, true))
	assert.Contains(t, enabled, readImageTool.Name)
	assert.Contains(t, enabled, contextGatherForgetTool.Name)
	assert.Contains(t, enabled, contextGatherRememberTool.Name)
	assert.NotContains(t, enabled, runCommandTool.Name)
	assert.NotContains(t, enabled, setBaseBranchTool.Name)
	assert.NotContains(t, enabled, doneTool.Name)
}

type contextGatherWorkflowResult struct {
	HistoryLength int
	StreamCalls   int
}

func TestContextGatherWorkflowUsesVisibleLocalizationSubflowAndRequiresTerminalTool(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	streamCalls := 0
	persistedSubflows := make([]domain.Subflow, 0, 2)

	wrapper := func(ctx workflow.Context) (contextGatherWorkflowResult, error) {
		history := NewVersionedChatHistory(ctx, "context-gather-workspace")
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: "context-gather-workspace",
				GlobalState: &flow_action.GlobalState{},
				FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodeLocalizationKey: {{Provider: "test", Model: "localization-model"}},
				common.CodingKey:           {{Provider: "test", Model: "coding-model"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return contextGatherWorkflowResult{}, err
		}

		err := GatherContextForCoding(dCtx, history, InitialCodeInfo{
			Requirements: "Workflow requirements marker",
		})
		return contextGatherWorkflowResult{
			HistoryLength: history.Len(),
			StreamCalls:   streamCalls,
		}, err
	}
	env.RegisterWorkflow(wrapper)
	env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedSubflows = append(persistedSubflows, args.Get(1).(domain.Subflow))
		}).
		Return(nil).
		Twice()
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).
		Return(nil).
		Times(4)
	env.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	env.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: "user"}, nil,
	).Maybe()

	var flagActivities *fflag.FFlagActivities
	env.OnActivity(
		flagActivities.EvalBoolFlag,
		mock.Anything,
		mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
			return params.FlagName == fflag.ContextGatheringForget
		}),
	).Return(false, nil).Once()

	var llmActivities *persisted_ai.Llm2Activities
	env.OnActivity(llmActivities.Stream, mock.Anything, mock.MatchedBy(func(input persisted_ai.StreamInput) bool {
		require.Equal(t, "localization-model", input.Options.ModelConfig.Model)
		require.NotEqual(t, "coding-model", input.Options.ModelConfig.Model)
		require.True(t, streamInputHasTool(input, contextGatherReadyTool.Name))
		require.True(t, streamInputHasTool(input, getHelpOrInputTool.Name))
		require.True(t, streamInputHasTool(input, bulkSearchRepositoryTool.Name))
		require.True(t, streamInputHasTool(input, currentGetSymbolDefinitionsTool().Name))
		require.True(t, streamInputHasTool(input, bulkReadFileTool.Name))
		require.False(t, streamInputHasTool(input, runCommandTool.Name))
		require.False(t, streamInputHasTool(input, setBaseBranchTool.Name))
		require.False(t, streamInputHasTool(input, doneTool.Name))
		require.False(t, streamInputHasTool(input, contextGatherForgetTool.Name))
		require.False(t, streamInputHasTool(input, contextGatherRememberTool.Name))
		return true
	})).Return(func(_ context.Context, _ persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
		streamCalls++
		if streamCalls == 1 {
			return &llm2.MessageResponse{
				StopReason: "stop",
				Output: llm2.Message{
					Role: llm2.RoleAssistant,
					Content: []llm2.ContentBlock{{
						Type: llm2.ContentBlockTypeText,
						Text: "I should continue exploring rather than terminate.",
					}},
				},
			}, nil
		}
		return contextGatherTerminalResponse("ready-call", contextGatherReadyTool.Name), nil
	}).Twice()

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result contextGatherWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 2, result.StreamCalls)
	assert.Zero(t, result.HistoryLength)

	require.Len(t, persistedSubflows, 2)
	assert.Equal(t, "gather_context", *persistedSubflows[0].Type)
	assert.Equal(t, "Gather Context", persistedSubflows[0].Name)
	assert.Equal(t, domain.SubflowStatusStarted, persistedSubflows[0].Status)
	assert.Equal(t, domain.SubflowStatusComplete, persistedSubflows[1].Status)
}

func TestContextGatherHelpTerminatesWithoutUserRequest(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	wrapper := func(ctx workflow.Context) (int, error) {
		history := NewVersionedChatHistory(ctx, "context-gather-help-workspace")
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: "context-gather-help-workspace",
				GlobalState: &flow_action.GlobalState{},
				FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodeLocalizationKey: {{Provider: "test", Model: "localization-model"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return 0, err
		}
		err := GatherContextForCoding(dCtx, history, InitialCodeInfo{Requirements: "Help terminal test"})
		return history.Len(), err
	}
	env.RegisterWorkflow(wrapper)
	env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Twice()
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Twice()
	env.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	env.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: "user"}, nil,
	).Maybe()

	var flagActivities *fflag.FFlagActivities
	env.OnActivity(
		flagActivities.EvalBoolFlag,
		mock.Anything,
		mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
			return params.FlagName == fflag.ContextGatheringForget
		}),
	).Return(false, nil).Once()

	var llmActivities *persisted_ai.Llm2Activities
	env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).
		Return(contextGatherTerminalResponse("help-call", getHelpOrInputTool.Name), nil).
		Once()

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var historyLength int
	require.NoError(t, env.GetWorkflowResult(&historyLength))
	assert.Zero(t, historyLength)
}

func contextGatherTerminalResponse(id, name string) *llm2.MessageResponse {
	arguments := "{}"
	if name == getHelpOrInputTool.Name {
		arguments = `{"requests":[{"content":"Proceed with the available repository context.","selfHelp":{"analysis":"Repository exploration cannot resolve the remaining question.","tools":[],"alreadyAttemptedTools":[]}}]}`
	}
	return &llm2.MessageResponse{
		StopReason: "tool_use",
		Output: llm2.Message{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id:        id,
					Name:      name,
					Arguments: arguments,
				},
			}},
		},
	}
}
