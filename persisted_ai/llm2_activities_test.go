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
		Params: llm2.Params{
			Tools: []*common.Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
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
				Params: llm2.Params{
					Tools: []*common.Tool{},
					ModelConfig: common.ModelConfig{
						Provider:        "openai",
						Model:           "o1",
						ReasoningEffort: tt.reasoningEffort,
					},
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
		Params: llm2.Params{
			Tools: []*common.Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-latest",
			},
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
				Params: llm2.Params{
					Tools:     []*common.Tool{},
					MaxTokens: tt.maxTokens,
					ModelConfig: common.ModelConfig{
						Provider: "anthropic",
						Model:    "claude-3-5-sonnet-latest",
					},
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
		Params: llm2.Params{
			Tools: []*common.Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
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
				Params: llm2.Params{
					Tools: []*common.Tool{},
					ModelConfig: common.ModelConfig{
						Provider:    "openai",
						Model:       "gpt-4",
						ServiceTier: tt.serviceTier,
					},
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
			Params: llm2.Params{
				Tools: []*common.Tool{},
				ModelConfig: common.ModelConfig{
					Provider: "openai",
					Model:    "gpt-4",
				},
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
