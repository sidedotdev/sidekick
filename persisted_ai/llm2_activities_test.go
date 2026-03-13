package persisted_ai

import (
	"testing"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/secret_manager"
)

func newTestStreamInput(options llm2.Options) StreamInput {
	return StreamInput{
		Options: options,
		Secrets: secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
	}
}

func TestStreamInputActionParams_OmitsReasoningEffortWhenEmpty(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4",
		},
	})

	params := si.ActionParams()

	if _, exists := params["reasoningEffort"]; exists {
		t.Errorf("Expected reasoningEffort key to be absent when ReasoningEffort is empty, but it was present")
	}
	if params["provider"] != "openai" {
		t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
	}
	if params["model"] != "gpt-4" {
		t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesReasoningEffortWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		reasoningEffort string
	}{
		{"low reasoning effort", "low"},
		{"medium reasoning effort", "medium"},
		{"high reasoning effort", "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools: []*common.Tool{},
				ModelConfig: common.ModelConfig{
					Provider:        "openai",
					Model:           "o1",
					ReasoningEffort: tt.reasoningEffort,
				},
			})

			params := si.ActionParams()

			reasoningEffort, exists := params["reasoningEffort"]
			if !exists {
				t.Errorf("Expected reasoningEffort key to be present when ReasoningEffort is '%s', but it was absent", tt.reasoningEffort)
			}
			if reasoningEffort != tt.reasoningEffort {
				t.Errorf("Expected reasoningEffort to be '%s', got %v", tt.reasoningEffort, reasoningEffort)
			}
			if params["provider"] != "openai" {
				t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
			}
			if params["model"] != "o1" {
				t.Errorf("Expected model to be 'o1', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_OmitsMaxTokensWhenUnset(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet-latest",
		},
	})

	params := si.ActionParams()

	if _, exists := params["maxTokens"]; exists {
		t.Errorf("Expected maxTokens key to be absent when MaxTokens is unset, but it was present")
	}
	if params["provider"] != "anthropic" {
		t.Errorf("Expected provider to be 'anthropic', got %v", params["provider"])
	}
	if params["model"] != "claude-3-5-sonnet-latest" {
		t.Errorf("Expected model to be 'claude-3-5-sonnet-latest', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesMaxTokensWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		maxTokens int
	}{
		{"small max tokens", 123},
		{"medium max tokens", 4000},
		{"large max tokens", 8000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools:     []*common.Tool{},
				MaxTokens: tt.maxTokens,
				ModelConfig: common.ModelConfig{
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet-latest",
				},
			})

			params := si.ActionParams()

			maxTokens, exists := params["maxTokens"]
			if !exists {
				t.Errorf("Expected maxTokens key to be present when MaxTokens is %d, but it was absent", tt.maxTokens)
			}
			if maxTokens != float64(tt.maxTokens) {
				t.Errorf("Expected maxTokens to be %d, got %v", tt.maxTokens, maxTokens)
			}
			if params["provider"] != "anthropic" {
				t.Errorf("Expected provider to be 'anthropic', got %v", params["provider"])
			}
			if params["model"] != "claude-3-5-sonnet-latest" {
				t.Errorf("Expected model to be 'claude-3-5-sonnet-latest', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_OmitsServiceTierWhenEmpty(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4",
		},
	})

	params := si.ActionParams()

	if _, exists := params["serviceTier"]; exists {
		t.Errorf("Expected serviceTier key to be absent when ServiceTier is empty, but it was present")
	}
	if params["provider"] != "openai" {
		t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
	}
	if params["model"] != "gpt-4" {
		t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesServiceTierWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		serviceTier string
	}{
		{"default service tier", "default"},
		{"flex service tier", "flex"},
		{"priority service tier", "priority"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools: []*common.Tool{},
				ModelConfig: common.ModelConfig{
					Provider:    "openai",
					Model:       "gpt-4",
					ServiceTier: tt.serviceTier,
				},
			})

			params := si.ActionParams()

			serviceTier, exists := params["serviceTier"]
			if !exists {
				t.Errorf("Expected serviceTier key to be present when ServiceTier is '%s', but it was absent", tt.serviceTier)
			}
			if serviceTier != tt.serviceTier {
				t.Errorf("Expected serviceTier to be '%s', got %v", tt.serviceTier, serviceTier)
			}
			if params["provider"] != "openai" {
				t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
			}
			if params["model"] != "gpt-4" {
				t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_IncludesMessagesAndSecretType(t *testing.T) {
	t.Parallel()
	si := StreamInput{
		Options: llm2.Options{
			Tools: []*common.Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
		},
		Secrets:     secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
		ChatHistory: &ChatHistoryContainer{},
	}

	params := si.ActionParams()

	if _, exists := params["messages"]; !exists {
		t.Error("Expected messages key to be present")
	}
	if _, exists := params["secretManagerType"]; !exists {
		t.Error("Expected secretManagerType key to be present")
	}
}
func TestGetLlm2Provider_AnthropicVariants(t *testing.T) {
	t.Parallel()

	providers := []common.ModelProviderPublicConfig{
		{
			Name:       "anthropic-proxy",
			Type:       "anthropic",
			BaseURL:    "https://proxy.example.com",
			DefaultLLM: "claude-sonnet-4-5",
		},
		{
			Name:       "vendor-anthropic",
			Type:       "anthropic_compatible",
			BaseURL:    "https://vendor.example.com",
			DefaultLLM: "vendor-model-v1",
		},
	}

	tests := []struct {
		name   string
		config common.ModelConfig
		want   llm2.AnthropicProvider
	}{
		{
			name: "anthropic proxy preserves anthropic model assumptions",
			config: common.ModelConfig{
				Provider: "anthropic-proxy",
			},
			want: llm2.AnthropicProvider{
				BaseURL:      "https://proxy.example.com",
				DefaultModel: "claude-sonnet-4-5",
			},
		},
		{
			name: "anthropic compatible disables anthropic model assumptions",
			config: common.ModelConfig{
				Provider: "vendor-anthropic",
			},
			want: llm2.AnthropicProvider{
				BaseURL:             "https://vendor.example.com",
				DefaultModel:        "vendor-model-v1",
				AnthropicCompatible: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider, err := getLlm2Provider(tt.config, providers)
			if err != nil {
				t.Fatalf("getLlm2Provider returned error: %v", err)
			}

			anthropicProvider, ok := provider.(llm2.AnthropicProvider)
			if !ok {
				t.Fatalf("provider type = %T, want llm2.AnthropicProvider", provider)
			}
			if anthropicProvider.BaseURL != tt.want.BaseURL ||
				anthropicProvider.DefaultModel != tt.want.DefaultModel ||
				anthropicProvider.AnthropicCompatible != tt.want.AnthropicCompatible ||
				anthropicProvider.AuthType != tt.want.AuthType {
				t.Fatalf("provider = %#v, want %#v", anthropicProvider, tt.want)
			}
		})
	}
}

func TestGetLlm2Provider_AuthType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    common.ModelConfig
		providers []common.ModelProviderPublicConfig
		assert    func(t *testing.T, provider llm2.Provider)
	}{
		{
			name: "builtin anthropic defaults to any",
			config: common.ModelConfig{
				Provider: "anthropic",
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeAny {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAny, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "builtin anthropic config uses explicit api type",
			config: common.ModelConfig{
				Provider: "anthropic",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "anthropic",
					Type:     "anthropic",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "named anthropic alias uses explicit subscription type",
			config: common.ModelConfig{
				Provider: "anthropic-subscription",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "anthropic-subscription",
					Type:     "anthropic",
					AuthType: common.ProviderAuthTypeSubscription,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeSubscription {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeSubscription, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "builtin openai config propagates auth type",
			config: common.ModelConfig{
				Provider: "openai",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "openai",
					Type:     "openai",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				openAIProvider, ok := provider.(llm2.OpenAIResponsesProvider)
				if !ok {
					t.Fatalf("expected llm2.OpenAIResponsesProvider, got %T", provider)
				}
				if openAIProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, openAIProvider.AuthType)
				}
			},
		},
		{
			name: "named openai compatible alias propagates auth type",
			config: common.ModelConfig{
				Provider: "workspace-openai",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:       "workspace-openai",
					Type:       "openai_compatible",
					BaseURL:    "https://example.com/v1",
					DefaultLLM: "gpt-4.1-mini",
					AuthType:   common.ProviderAuthTypeSubscription,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				openAIProvider, ok := provider.(llm2.OpenAIProvider)
				if !ok {
					t.Fatalf("expected llm2.OpenAIProvider, got %T", provider)
				}
				if openAIProvider.AuthType != common.ProviderAuthTypeSubscription {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeSubscription, openAIProvider.AuthType)
				}
			},
		},
		{
			name: "named google alias propagates auth type",
			config: common.ModelConfig{
				Provider: "workspace-google",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "workspace-google",
					Type:     "google",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				googleProvider, ok := provider.(llm2.GoogleProvider)
				if !ok {
					t.Fatalf("expected llm2.GoogleProvider, got %T", provider)
				}
				if googleProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, googleProvider.AuthType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider, err := getLlm2Provider(tt.config, tt.providers)
			if err != nil {
				t.Fatalf("getLlm2Provider returned error: %v", err)
			}

			tt.assert(t, provider)
		})
	}
}
