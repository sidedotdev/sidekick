package persisted_ai

import (
	"context"
	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/srv/sqlite"
	"sidekick/utils"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type forcedToolCallTestConfig struct {
	StreamInput
	resolveModelConfig ModelConfigResolver
	resolveTools       func(common.ModelConfig) []*common.Tool
	historical         bool
}

func runForcedToolCallTest(
	t *testing.T,
	config forcedToolCallTestConfig,
	stream func(context.Context, StreamInput) (*llm2.MessageResponse, error),
) (common.MessageResponse, *ChatHistoryContainer, error) {
	t.Helper()

	storage := sqlite.NewTestSqliteStorage(t, "forced-tool-call")
	t.Cleanup(func() {
		require.NoError(t, storage.Close())
	})
	require.NoError(t, config.ChatHistory.Persist(context.Background(), storage, func() string {
		return ksuid.New().String()
	}))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(3 * time.Minute)
	if config.historical {
		env.OnGetVersion("force-tool-call-prompt", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	}
	historyActivities := &ChatHistoryActivities{Storage: storage}
	env.RegisterActivity(historyActivities.AppendMessage)
	env.RegisterActivity(RepairToolCallArgumentsActivity)
	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	var activities *Llm2Activities
	env.OnActivity(activities.Stream, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input StreamInput) (*llm2.MessageResponse, error) {
			if err := input.ChatHistory.Hydrate(ctx, storage); err != nil {
				return nil, err
			}
			return stream(ctx, input)
		},
	)

	var response common.MessageResponse
	testWorkflow := func(ctx workflow.Context) error {
		eCtx := flow_action.ExecContext{
			Context:     utils.NoRetryCtx(ctx),
			WorkspaceId: config.WorkspaceId,
			FlowScope:   &flow_action.FlowScope{SubflowName: "forced-tool-call"},
			GlobalState: &flow_action.GlobalState{},
			Secrets:     &config.Secrets,
			Providers:   config.Providers,
		}
		var err error
		response, err = forceToolCallWithToolsResolver(
			eCtx.NewActionContext("forced-tool-call"),
			flow_action.TrackOptions{},
			config.Options.ModelConfig,
			config.resolveModelConfig,
			config.resolveTools,
			config.ChatHistory,
			config.ToolNameMapping,
			config.Options.ParallelToolCalls,
			true,
			config.Options.Tools...,
		)
		return err
	}
	env.ExecuteWorkflow(testWorkflow)
	env.AssertExpectations(t)

	// Round-trip the refs so assertions observe the persisted conversation,
	// rather than the workflow's potentially stale hydrated messages.
	data, err := config.ChatHistory.MarshalJSON()
	require.NoError(t, err)
	history := &ChatHistoryContainer{}
	require.NoError(t, history.UnmarshalJSON(data))
	require.NoError(t, history.Hydrate(context.Background(), storage))
	return response, history, env.GetWorkflowError()
}

func TestForceToolCallPreparesRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		providerType string
		model        string
		multiple     bool
		mapped       bool
		retry        bool
		historical   bool
		wantChoice   common.ToolChoice
	}{
		{name: "haiku", providerType: "anthropic", model: "claude-haiku-5-5", wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto}},
		{name: "compatible", providerType: "anthropic_compatible", model: "custom", wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto}},
		{name: "older anthropic", providerType: "anthropic", model: "claude-opus-4-5", wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "record_plan"}},
		{name: "openai", providerType: "openai", model: "gpt-5", wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "record_plan"}},
		{name: "multiple tools", providerType: "google", model: "gemini", multiple: true, wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeRequired}},
		{name: "mapped tool", providerType: "anthropic", model: "claude-haiku-5-5", mapped: true, wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto}},
		{name: "retry", providerType: "anthropic", model: "claude-haiku-5-5", retry: true, wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto}},
		{name: "historical workflow", providerType: "anthropic", model: "claude-haiku-5-5", historical: true, wantChoice: common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "record_plan"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := newRunawayStreamActivityInput(t)
			input.Options.ModelConfig = common.ModelConfig{Provider: "test", Model: tt.model}
			input.Options.Tools = []*common.Tool{{Name: "record_plan"}}
			input.Providers = []common.ModelProviderPublicConfig{{Name: "test", Type: tt.providerType}}
			if tt.multiple {
				input.Options.Tools = append(input.Options.Tools, &common.Tool{Name: "cancel_plan"})
			}
			if tt.mapped {
				input.ToolNameMapping = &ToolNameMappingConfig{Prefix: "mapped_"}
			}
			originalMessages := input.ChatHistory.History.(*Llm2ChatHistory).Llm2Messages()
			var requests []StreamInput
			_, history, err := runForcedToolCallTest(t, forcedToolCallTestConfig{
				StreamInput: input,
				historical:  tt.historical,
			}, func(_ context.Context, request StreamInput) (*llm2.MessageResponse, error) {
				requests = append(requests, request)
				content := []llm2.ContentBlock{{
					Type:    llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{Id: "call", Name: "record_plan", Arguments: "{}"},
				}}
				if tt.retry && len(requests) == 1 {
					content = llm2.TextContentBlocks("I will record a plan.")
				}
				return &llm2.MessageResponse{Output: llm2.Message{Role: llm2.RoleAssistant, Content: content}}, nil
			})
			requireWorkflowSuccess(t, err)
			wantAttempts := 1
			if tt.retry {
				wantAttempts = 2
			}
			require.Len(t, requests, wantAttempts)
			for i, request := range requests {
				require.Equal(t, tt.wantChoice, request.Options.ToolChoice)
				messages := request.ChatHistory.History.(*Llm2ChatHistory).Llm2Messages()
				if tt.historical {
					require.Equal(t, originalMessages, messages)
					continue
				}
				require.Len(t, messages, len(originalMessages)+1+2*i)
				instruction := messages[len(messages)-1]
				require.Equal(t, llm2.RoleSystem, instruction.Role)
				require.Len(t, instruction.Content, 1)
				text := instruction.Content[0].Text
				require.Contains(t, text, "MUST")
				if tt.multiple {
					require.Contains(t, text, "one of the provided tools")
				} else if tt.mapped {
					require.Contains(t, text, "mapped_record_plan")
				} else {
					require.Contains(t, text, "record_plan")
				}
			}
			messages := history.History.(*Llm2ChatHistory).Llm2Messages()
			require.Len(t, messages, len(originalMessages)+1+2*(wantAttempts-1))
			for _, message := range messages {
				require.NotContains(t, message.GetContentString(), "MUST")
			}
		})
	}
}

func TestForceToolCallResolvesEachAttempt(t *testing.T) {
	t.Parallel()

	input := newRunawayStreamActivityInput(t)
	input.Options.ModelConfig = common.ModelConfig{Provider: "test", Model: "initial"}
	input.Options.Tools = []*common.Tool{{Name: "initial_tool"}}
	input.Providers = []common.ModelProviderPublicConfig{{Name: "test", Type: "anthropic"}}
	models := []string{"claude-haiku-5-5", "claude-opus-4-5"}
	resolutions := 0
	var requests []StreamInput
	_, _, err := runForcedToolCallTest(t, forcedToolCallTestConfig{
		StreamInput: input,
		resolveModelConfig: func() common.ModelConfig {
			model := models[min(resolutions, len(models)-1)]
			resolutions++
			return common.ModelConfig{Provider: "test", Model: model}
		},
		resolveTools: func(config common.ModelConfig) []*common.Tool {
			if config.Model == models[0] {
				return []*common.Tool{{Name: "record_plan"}, {Name: "cancel_plan"}}
			}
			return []*common.Tool{{Name: "submit_plan"}}
		},
	}, func(_ context.Context, request StreamInput) (*llm2.MessageResponse, error) {
		requests = append(requests, request)
		content := llm2.TextContentBlocks("I will record a plan.")
		if len(requests) > 1 {
			content = []llm2.ContentBlock{{
				Type:    llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{Id: "call", Name: "submit_plan", Arguments: "{}"},
			}}
		}
		return &llm2.MessageResponse{Output: llm2.Message{Role: llm2.RoleAssistant, Content: content}}, nil
	})
	requireWorkflowSuccess(t, err)
	require.Len(t, requests, 2)
	require.Equal(t, 2, resolutions)
	require.Equal(t, models[0], requests[0].Options.Model)
	require.Equal(t, common.ToolChoice{Type: common.ToolChoiceTypeAuto}, requests[0].Options.ToolChoice)
	require.Len(t, requests[0].Options.Tools, 2)
	messages := requests[0].ChatHistory.History.(*Llm2ChatHistory).Llm2Messages()
	require.Contains(t, messages[len(messages)-1].GetContentString(), "one of the provided tools")

	require.Equal(t, models[1], requests[1].Options.Model)
	require.Equal(t, common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "submit_plan"}, requests[1].Options.ToolChoice)
	require.Len(t, requests[1].Options.Tools, 1)
	messages = requests[1].ChatHistory.History.(*Llm2ChatHistory).Llm2Messages()
	require.Contains(t, messages[len(messages)-1].GetContentString(), "submit_plan")
	for _, message := range messages {
		require.NotContains(t, message.GetContentString(), "one of the provided tools")
	}
}
