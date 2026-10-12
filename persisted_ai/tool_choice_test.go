package persisted_ai

import (
	"context"
	"sidekick/common"
	"sidekick/llm2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamActivity_ResolvesToolChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		provider     string
		providerType string
		model        string
		defaultModel string
		wantAuto     bool
	}{
		{name: "haiku 5.5", provider: "anthropic", model: "claude-haiku-5-5", wantAuto: true},
		{name: "case insensitive haiku 5.5", provider: "anthropic", model: "CLAUDE-HAIKU-5.5", wantAuto: true},
		{name: "sonnet 5.5", provider: "anthropic", model: "claude-sonnet-5-5", wantAuto: true},
		{name: "5.1 dotted", provider: "anthropic", model: "claude-sonnet-5.1", wantAuto: true},
		{name: "5.1 hyphenated", provider: "anthropic", model: "claude-5-1-sonnet", wantAuto: true},
		{name: "future major", provider: "anthropic", model: "claude-opus-6", wantAuto: true},
		{name: "5.0", provider: "anthropic", model: "claude-sonnet-5-0"},
		{name: "4.5", provider: "anthropic", model: "claude-opus-4-5"},
		{name: "dated model", provider: "anthropic", model: "claude-sonnet-4-20250514"},
		{name: "date without version", provider: "anthropic", model: "claude-sonnet-20250514"},
		{name: "unversioned model", provider: "anthropic", model: "some-custom-model"},
		{name: "non claude model", provider: "anthropic", model: "sonnet-5-5"},
		{name: "provider alias", provider: "custom", providerType: "anthropic", model: "claude-sonnet-5-5", wantAuto: true},
		{name: "configured default model", provider: "custom", providerType: "anthropic", defaultModel: "claude-sonnet-5-5", wantAuto: true},
		{name: "explicit model overrides default", provider: "custom", providerType: "anthropic", model: "claude-opus-4-5", defaultModel: "claude-sonnet-5-5"},
		{name: "built in default model", provider: "anthropic"},
		{name: "compatible old model", provider: "custom", providerType: "anthropic_compatible", model: "claude-opus-4-5", wantAuto: true},
		{name: "compatible non claude model", provider: "custom", providerType: "anthropic_compatible", model: "glm-4.6", wantAuto: true},
		{name: "compatible haiku", provider: "custom", providerType: "anthropic_compatible", model: "claude-haiku-5-5", wantAuto: true},
		{name: "compatible default model", provider: "custom", providerType: "anthropic_compatible", defaultModel: "glm-4.6", wantAuto: true},
		{name: "openai", provider: "openai", model: "claude-sonnet-5-5"},
		{name: "google", provider: "google", model: "claude-sonnet-5-5"},
		{name: "bedrock", provider: "bedrock", model: "global.anthropic.claude-sonnet-5-5-v1:0"},
		{name: "openai compatible", provider: "custom", providerType: "openai_compatible", model: "claude-sonnet-5-5"},
		{name: "openai responses compatible", provider: "custom", providerType: "openai_responses_compatible", model: "claude-sonnet-5-5"},
		{name: "configured type overrides name", provider: "anthropic", providerType: "openai_compatible", model: "claude-sonnet-5-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			choices := []common.ToolChoice{
				{Type: common.ToolChoiceTypeTool, Name: "record_plan"},
				{Type: common.ToolChoiceTypeRequired},
				{Type: common.ToolChoiceTypeAuto},
				{},
			}
			for _, choice := range choices {
				t.Run(string(choice.Type), func(t *testing.T) {
					t.Parallel()

					input := newRunawayStreamActivityInput(t)
					input.Options.Provider = tt.provider
					input.Options.Model = tt.model
					input.Options.ToolChoice = choice
					input.Options.Tools = []*common.Tool{{Name: "record_plan"}}
					input.Options.ReasoningEffort = "high"
					parallel := true
					input.Options.ParallelToolCalls = &parallel
					forced := choice.Type == common.ToolChoiceTypeTool || choice.Type == common.ToolChoiceTypeRequired
					input.InstructToolCall = forced
					if tt.providerType != "" {
						input.Providers = []common.ModelProviderPublicConfig{{
							Name:       tt.provider,
							Type:       tt.providerType,
							DefaultLLM: tt.defaultModel,
						}}
					}
					originalOptions := input.Options
					history := input.ChatHistory.History.(*Llm2ChatHistory)
					originalMessages := history.Llm2Messages()

					provider := &scriptedProvider{attempts: [][]llm2.Event{{}}}
					activities := Llm2Activities{
						providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
							return provider, nil
						},
					}
					_, err := activities.Stream(context.Background(), input)
					require.NoError(t, err)
					require.Len(t, provider.requests, 1)

					request := provider.requests[0]
					wantOptions := originalOptions
					if tt.wantAuto && forced {
						wantOptions.ToolChoice = common.ToolChoice{Type: common.ToolChoiceTypeAuto}
					}
					require.Equal(t, wantOptions, request.Options)
					require.Equal(t, originalOptions, input.Options)
					require.Equal(t, originalMessages, history.Llm2Messages())

					if forced {
						require.Len(t, request.Messages, len(originalMessages)+1)
						instruction := request.Messages[len(originalMessages)]
						require.Equal(t, llm2.RoleSystem, instruction.Role)
						require.Len(t, instruction.Content, 1)
						require.Contains(t, instruction.Content[0].Text, "MUST")
						if choice.Type == common.ToolChoiceTypeTool {
							require.Contains(t, instruction.Content[0].Text, "record_plan")
						} else {
							require.Contains(t, instruction.Content[0].Text, "one of the provided tools")
						}
					} else {
						require.Equal(t, originalMessages, request.Messages)
					}
				})
			}
		})
	}
}
