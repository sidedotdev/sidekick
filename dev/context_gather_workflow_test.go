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
	"sidekick/srv"
	"sidekick/utils"
	"strings"
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

		prompt, err := renderContextGatherPrompt(DevContext{}, InitialCodeInfo{
			Requirements: "Complete basic requirements marker",
		}, false, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Complete basic requirements marker")
		assert.Contains(t, prompt, "all repository context needed")
		assert.Contains(t, prompt, contextGatherReadyTool.Name)
		assert.Contains(t, prompt, getHelpOrInputTool.Name)
		assert.NotContains(t, prompt, contextGatherForgetTool.Name)
		assert.NotContains(t, prompt, startInitialCodeContext)
		assert.NotContains(t, prompt, "Environment:")
	})

	t.Run("includes repo summary, hints and environment context", func(t *testing.T) {
		t.Parallel()

		dCtx := DevContext{
			RepoConfig: common.RepoConfig{
				EditCode: common.EditCodeConfig{Hints: "Repo hints marker"},
			},
		}
		prompt, err := renderContextGatherPrompt(dCtx, InitialCodeInfo{
			CodeContext:  "Ranked summary marker",
			Requirements: "Complete basic requirements marker",
		}, false, "OS: testos, Arch: testarch")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Repo hints marker")
		assert.Contains(t, prompt, "Environment: OS: testos, Arch: testarch")
		assert.Contains(t, prompt, startInitialCodeContext)
		assert.Contains(t, prompt, "Ranked summary marker")
		assert.Contains(t, prompt, endInitialCodeContext)
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
		prompt, err := renderContextGatherPrompt(DevContext{}, InitialDevStepInfo{
			Requirements:  "Complete planned requirements marker",
			PlanExecution: DevPlanExecution{Plan: plan},
			Step:          step,
		}, true, "")
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

		_, err := GatherContextForCoding(dCtx, history, InitialCodeInfo{
			Requirements: "Workflow requirements marker",
		}, ContextGatherOptions{})
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
		_, err := GatherContextForCoding(dCtx, history, InitialCodeInfo{Requirements: "Help terminal test"}, ContextGatherOptions{})
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

// newContextGatherPauseTestDevContext builds a DevContext for pause-handling
// workflow tests and exposes its GlobalState for pause manipulation.
func newContextGatherPauseTestDevContext(ctx workflow.Context, workspaceId string) (DevContext, *flow_action.GlobalState, error) {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     ctx,
			WorkspaceId: workspaceId,
			GlobalState: gs,
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
		return DevContext{}, nil, err
	}
	return dCtx, gs, nil
}

// TestContextGatherPauseKeepsResponseAndRequestsGuidance covers the fixed
// behavior: a pause landing while a stream is in flight must not drop the
// completed response or its tool call, and the pause is resolved via a user
// request before the next stream.
func TestContextGatherPauseKeepsResponseAndRequestsGuidance(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	streamCalls := 0
	streamLens := []int{}
	userPromptCount := 0
	var gatherState *flow_action.GlobalState

	childWorkflow := func(ctx workflow.Context) error {
		history := NewVersionedChatHistory(ctx, "context-gather-pause-workspace")
		dCtx, gs, err := newContextGatherPauseTestDevContext(ctx, "context-gather-pause-workspace")
		if err != nil {
			return err
		}
		gatherState = gs
		_, err = GatherContextForCoding(dCtx, history, InitialCodeInfo{Requirements: "Pause handling test"}, ContextGatherOptions{})
		return err
	}
	env.RegisterWorkflow(childWorkflow)

	parentWorkflow := func(ctx workflow.Context) error {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			userPromptCount++
			_ = workflow.SignalExternalWorkflow(
				ctx,
				req.OriginWorkflowId,
				"",
				flow_action.UserResponseSignalName(req.FlowActionId),
				flow_action.UserResponse{
					FlowActionId: req.FlowActionId,
					Content:      "resume guidance marker",
				},
			).Get(ctx, nil)
		})
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "context-gather-pause-child",
		})
		return workflow.ExecuteChildWorkflow(childCtx, childWorkflow).Get(ctx, nil)
	}
	env.RegisterWorkflow(parentWorkflow)
	env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Twice()
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var srvActivities srv.Activities
	env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	appendedMessages := make([]llm2.Message, 0)
	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	env.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			appendedMessages = append(appendedMessages, input.Message)
			return &persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: string(input.Message.Role)}, nil
		}).Maybe()

	var flagActivities *fflag.FFlagActivities
	env.OnActivity(flagActivities.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			streamCalls++
			streamLens = append(streamLens, input.ChatHistory.Len())
			if streamCalls == 1 {
				// simulate a pause signal landing while the stream is in flight
				gatherState.Paused = true
				return contextGatherTerminalResponse("paused-call", "run_command"), nil
			}
			return contextGatherTerminalResponse("ready-call", contextGatherReadyTool.Name), nil
		}).Twice()

	env.ExecuteWorkflow(parentWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, 2, streamCalls)
	assert.Equal(t, 1, userPromptCount)
	// prompt first, then assistant response, tool result and pause guidance are
	// all retained before the second stream
	assert.Equal(t, []int{1, 4}, streamLens)

	toolResultAppended := false
	guidanceAppended := false
	for _, message := range appendedMessages {
		for _, block := range message.Content {
			if block.Type == llm2.ContentBlockTypeToolResult && block.ToolResult != nil && block.ToolResult.ToolCallId == "paused-call" {
				toolResultAppended = true
			}
			if block.Type == llm2.ContentBlockTypeText && strings.Contains(block.Text, "resume guidance marker") {
				guidanceAppended = true
			}
		}
	}
	assert.True(t, toolResultAppended, "tool result for the paused response should be appended")
	assert.True(t, guidanceAppended, "pause guidance should be appended to chat history")
}

type contextGatherReminderRun struct {
	streamLens       []int
	streamOptions    []llm2.Options
	appendedMessages []llm2.Message
}

func toolNames(tools []*llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

type contextGatherReminderOptions struct {
	legacyVersion bool
	// proseTurns is how many responses lack tool calls before the phase ends.
	proseTurns int
	// remindAfter, escalateEvery and maxIterations configure the
	// "code_localization" agent use case when set.
	remindAfter   int
	escalateEvery int
	maxIterations int
	// neverReady keeps every response prose-only, so gathering only ends if it
	// is cut short.
	neverReady bool
}

// runContextGatherReminderWorkflow drives a gather phase whose first
// proseTurns responses are prose without tool calls, followed by a response
// that ends the phase.
func runContextGatherReminderWorkflow(t *testing.T, opts contextGatherReminderOptions) contextGatherReminderRun {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	run := contextGatherReminderRun{}
	streamCalls := 0

	repoConfig := common.RepoConfig{}
	if opts.remindAfter > 0 || opts.escalateEvery > 0 || opts.maxIterations > 0 {
		repoConfig.AgentConfig = map[string]common.AgentUseCaseConfig{
			common.CodeLocalizationKey: {
				RemindAfter:           opts.remindAfter,
				EscalateReminderEvery: opts.escalateEvery,
				MaxIterations:         opts.maxIterations,
			},
		}
	}

	wrapper := func(ctx workflow.Context) error {
		history := NewVersionedChatHistory(ctx, "context-gather-reminder-workspace")
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: "context-gather-reminder-workspace",
				GlobalState: &flow_action.GlobalState{},
				FlowScope:   &flow_action.FlowScope{SubflowName: "test"},
				Secrets: &secret_manager.SecretManagerContainer{
					SecretManager: secret_manager.MockSecretManager{},
				},
			},
			RepoConfig: repoConfig,
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
			UseCaseConfigs: map[string][]common.ModelConfig{
				common.CodeLocalizationKey: {{Provider: "test", Model: "localization-model"}},
			},
		})
		if err := SetupModelConfigHandlers(dCtx); err != nil {
			return err
		}
		_, err := GatherContextForCoding(dCtx, history, InitialCodeInfo{Requirements: "Reminder test"}, ContextGatherOptions{})
		return err
	}
	env.RegisterWorkflow(wrapper)
	env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
	if opts.legacyVersion {
		env.OnGetVersion("context-gather-ready-reminder", workflow.DefaultVersion, 1).
			Return(workflow.DefaultVersion).
			Maybe()
	}

	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Twice()
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	env.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			run.appendedMessages = append(run.appendedMessages, input.Message)
			return &persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: string(input.Message.Role)}, nil
		}).Maybe()

	var flagActivities *fflag.FFlagActivities
	env.OnActivity(flagActivities.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	streamCall := env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			streamCalls++
			run.streamLens = append(run.streamLens, input.ChatHistory.Len())
			run.streamOptions = append(run.streamOptions, input.Options)
			if opts.neverReady || streamCalls <= opts.proseTurns {
				return &llm2.MessageResponse{
					StopReason: "stop",
					Output: llm2.Message{
						Role: llm2.RoleAssistant,
						Content: []llm2.ContentBlock{{
							Type: llm2.ContentBlockTypeText,
							Text: "Still considering which files matter.",
						}},
					},
				}, nil
			}
			return contextGatherTerminalResponse("ready-call", contextGatherReadyTool.Name), nil
		})
	if opts.neverReady {
		streamCall.Maybe()
	} else {
		streamCall.Times(opts.proseTurns + 1)
	}

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	return run
}

// contextGatherSystemTexts collects the system-role reminders appended during
// the gather phase, in order.
func contextGatherSystemTexts(messages []llm2.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role != llm2.RoleSystem {
			continue
		}
		for _, block := range message.Content {
			if block.Type == llm2.ContentBlockTypeText {
				texts = append(texts, block.Text)
			}
		}
	}
	return texts
}

func countText(texts []string, wanted string) int {
	count := 0
	for _, text := range texts {
		if text == wanted {
			count++
		}
	}
	return count
}

func TestContextGatherRemindsToCallReadyWhenResponseHasNoToolCalls(t *testing.T) {
	run := runContextGatherReminderWorkflow(t, contextGatherReminderOptions{proseTurns: 1})

	reminders := contextGatherSystemTexts(run.appendedMessages)
	require.Len(t, reminders, 1)
	assert.Equal(t, contextGatherReadyReminder().Content, reminders[0])
	assert.Contains(t, reminders[0], contextGatherReadyTool.Name)
	// prompt first, then the assistant prose and the reminder are both visible
	// to the next stream
	assert.Equal(t, []int{1, 3}, run.streamLens)
}

func TestContextGatherLegacyVersionSkipsReadyReminder(t *testing.T) {
	run := runContextGatherReminderWorkflow(t, contextGatherReminderOptions{legacyVersion: true, proseTurns: 1})

	assert.Empty(t, contextGatherSystemTexts(run.appendedMessages))
	assert.Equal(t, []int{1, 2}, run.streamLens)
}

func TestContextGatherRemindsWhenGatheringRunsLong(t *testing.T) {
	limits := contextGatherLimits{remindAfter: 2, escalateEvery: 2, maxIterations: 100}
	// warning at iteration 2, escalations at 4 and 6
	proseTurns := 6
	run := runContextGatherReminderWorkflow(t, contextGatherReminderOptions{
		proseTurns:    proseTurns,
		remindAfter:   limits.remindAfter,
		escalateEvery: limits.escalateEvery,
		maxIterations: limits.maxIterations,
	})

	texts := contextGatherSystemTexts(run.appendedMessages)
	warning, ok := limits.reminder(limits.remindAfter, false)
	require.True(t, ok)
	strong, ok := limits.reminder(limits.remindAfter+limits.escalateEvery, false)
	require.True(t, ok)

	assert.Equal(t, 1, countText(texts, warning.Content))
	assert.Equal(t, 2, countText(texts, strong.Content))
	assert.Equal(t, proseTurns, countText(texts, contextGatherReadyReminder().Content))
}

func TestContextGatherForcesWrapUpThenHandsOffAtMaxIterations(t *testing.T) {
	limits := contextGatherLimits{remindAfter: 1, escalateEvery: 2, maxIterations: 3}
	run := runContextGatherReminderWorkflow(t, contextGatherReminderOptions{
		neverReady:    true,
		remindAfter:   limits.remindAfter,
		escalateEvery: limits.escalateEvery,
		maxIterations: limits.maxIterations,
	})

	// gathering hands off on its own once it spends a whole escalation
	// interval past the limit without calling ready
	require.Len(t, run.streamOptions, limits.maxIterations+limits.escalateEvery)
	for i, options := range run.streamOptions {
		completedIterations := i
		if !limits.wrapUpOnly(completedIterations) {
			assert.Equal(t, llm.ToolChoiceTypeAuto, options.ToolChoice.Type)
			assert.Contains(t, toolNames(options.Tools), bulkSearchRepositoryTool.Name)
			continue
		}
		assert.Equal(t, llm.ToolChoiceTypeRequired, options.ToolChoice.Type)
		assert.Equal(t, []string{contextGatherReadyTool.Name}, toolNames(options.Tools))
	}
}

func TestContextGatherLimitsFollowAgentConfig(t *testing.T) {
	t.Parallel()

	localizationConfig := func(cfg common.AgentUseCaseConfig) DevContext {
		return DevContext{
			RepoConfig: common.RepoConfig{
				AgentConfig: map[string]common.AgentUseCaseConfig{
					common.CodeLocalizationKey: cfg,
				},
			},
		}
	}
	defaults := contextGatherLimits{
		remindAfter:   defaultContextGatherRemindAfter,
		escalateEvery: defaultContextGatherEscalateReminderEvery,
		maxIterations: defaultContextGatherMaxIterations,
	}

	tests := []struct {
		name string
		dCtx DevContext
		want contextGatherLimits
	}{
		{
			name: "defaults when unconfigured",
			dCtx: DevContext{},
			want: defaults,
		},
		{
			name: "auto iterations do not affect reminders",
			dCtx: localizationConfig(common.AgentUseCaseConfig{AutoIterations: 4}),
			want: defaults,
		},
		{
			name: "configured first reminder keeps the other defaults",
			dCtx: localizationConfig(common.AgentUseCaseConfig{RemindAfter: 4}),
			want: contextGatherLimits{
				remindAfter:   4,
				escalateEvery: defaults.escalateEvery,
				maxIterations: defaults.maxIterations,
			},
		},
		{
			name: "configured escalation interval alone",
			dCtx: localizationConfig(common.AgentUseCaseConfig{EscalateReminderEvery: 2}),
			want: contextGatherLimits{
				remindAfter:   defaults.remindAfter,
				escalateEvery: 2,
				maxIterations: defaults.maxIterations,
			},
		},
		{
			name: "all configured",
			dCtx: localizationConfig(common.AgentUseCaseConfig{
				RemindAfter:           4,
				EscalateReminderEvery: 20,
				MaxIterations:         50,
			}),
			want: contextGatherLimits{remindAfter: 4, escalateEvery: 20, maxIterations: 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, contextGatherLimitsFor(tt.dCtx))
		})
	}
}

func TestContextGatherIterationReminderThresholds(t *testing.T) {
	t.Parallel()

	limits := contextGatherLimits{remindAfter: 4, escalateEvery: 3, maxIterations: 100}
	tests := []struct {
		name       string
		iterations int
		wantOk     bool
		wantStrong bool
	}{
		{name: "early iterations stay quiet", iterations: 1},
		{name: "just below the first reminder", iterations: limits.remindAfter - 1},
		{name: "first reminder", iterations: limits.remindAfter, wantOk: true},
		{name: "within the escalation interval", iterations: limits.remindAfter + 1},
		{
			name:       "first escalation",
			iterations: limits.remindAfter + limits.escalateEvery,
			wantOk:     true,
			wantStrong: true,
		},
		{
			name:       "off interval after escalating",
			iterations: limits.remindAfter + limits.escalateEvery + 1,
		},
		{
			name:       "next escalation",
			iterations: limits.remindAfter + 2*limits.escalateEvery,
			wantOk:     true,
			wantStrong: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reminder, ok := limits.reminder(tt.iterations, false)
			require.Equal(t, tt.wantOk, ok)
			if !tt.wantOk {
				return
			}
			assert.Equal(t, llm.ChatMessageRoleSystem, reminder.Role)
			assert.True(t, strings.HasPrefix(reminder.Content, "SYSTEM MESSAGE: "))
			assert.Contains(t, reminder.Content, contextGatherReadyTool.Name)
			assert.NotContains(t, reminder.Content, contextGatherForgetTool.Name)
			if tt.wantStrong {
				strong, _ := limits.reminder(limits.remindAfter+limits.escalateEvery, false)
				assert.Equal(t, strong.Content, reminder.Content)
			}
		})
	}
}

func TestContextGatherIterationReminderMentionsForgettingWhenEnabled(t *testing.T) {
	t.Parallel()

	limits := contextGatherLimitsFor(DevContext{})
	reminder, ok := limits.reminder(limits.remindAfter, true)
	require.True(t, ok)
	assert.Contains(t, reminder.Content, contextGatherForgetTool.Name)
	assert.Contains(t, reminder.Content, contextGatherReadyTool.Name)
}

func TestContextGatherExplainsRestrictedToolsBeforeTheFirstReminder(t *testing.T) {
	t.Parallel()

	limits := contextGatherLimits{remindAfter: 10, escalateEvery: 5, maxIterations: 3}
	reminder, ok := limits.reminder(limits.maxIterations, false)
	require.True(t, ok)
	assert.Contains(t, reminder.Content, "wrap-up tools")
	assert.Contains(t, reminder.Content, contextGatherReadyTool.Name)
}

func TestContextGatherWrapUpToolsOnlyAllowHandoffAdjustments(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{contextGatherReadyTool.Name}, toolNames(contextGatherWrapUpTools(false)))
	assert.Equal(t, []string{
		contextGatherReadyTool.Name,
		contextGatherForgetTool.Name,
		contextGatherRememberTool.Name,
	}, toolNames(contextGatherWrapUpTools(true)))
}
