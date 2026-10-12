package persisted_ai

import (
	"context"
	"sidekick/common"
	"sidekick/llm2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveToolChoice(t *testing.T) {
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

					options := llm2.Options{
						ModelConfig: common.ModelConfig{Provider: tt.provider, Model: tt.model},
						ToolChoice:  choice,
					}
					var providers []common.ModelProviderPublicConfig
					if tt.providerType != "" {
						providers = []common.ModelProviderPublicConfig{{
							Name:       tt.provider,
							Type:       tt.providerType,
							DefaultLLM: tt.defaultModel,
						}}
					}
					want := choice
					if tt.wantAuto && (choice.Type == common.ToolChoiceTypeTool || choice.Type == common.ToolChoiceTypeRequired) {
						want = common.ToolChoice{Type: common.ToolChoiceTypeAuto}
					}
					require.Equal(t, want, resolveToolChoice(options, providers))
					require.Equal(t, choice, options.ToolChoice)
				})
			}
		})
	}
}

func TestStreamActivityPreservesToolChoice(t *testing.T) {
	t.Parallel()

	for _, providerType := range []string{"anthropic", "anthropic_compatible", "openai", "google"} {
		t.Run(providerType, func(t *testing.T) {
			t.Parallel()

			for _, choice := range []common.ToolChoice{
				{Type: common.ToolChoiceTypeTool, Name: "record_plan"},
				{Type: common.ToolChoiceTypeRequired},
				{Type: common.ToolChoiceTypeAuto},
				{},
			} {
				t.Run(string(choice.Type), func(t *testing.T) {
					t.Parallel()

					input := newRunawayStreamActivityInput(t)
					input.Options.ModelConfig = common.ModelConfig{Provider: "test-provider", Model: "claude-haiku-5-5"}
					input.Options.ToolChoice = choice
					input.Options.Tools = []*common.Tool{{Name: "record_plan"}}
					input.Providers = []common.ModelProviderPublicConfig{{Name: "test-provider", Type: providerType}}
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
					require.Equal(t, originalOptions, provider.requests[0].Options)
					require.Equal(t, originalMessages, provider.requests[0].Messages)
					require.Equal(t, originalOptions, input.Options)
					require.Equal(t, originalMessages, history.Llm2Messages())
				})
			}
		})
	}
}
