package llm

import (
	"testing"

	"sidekick/common"
)

func TestActionParams_OmitsReasoningEffortWhenEmpty(t *testing.T) {
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
			Tools: []*Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
		},
	}

	params := options.ActionParams()

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

func TestActionParams_IncludesReasoningEffortWhenSet(t *testing.T) {
	tests := []struct {
		name            string
		reasoningEffort string
	}{
		{
			name:            "low reasoning effort",
			reasoningEffort: "low",
		},
		{
			name:            "medium reasoning effort",
			reasoningEffort: "medium",
		},
		{
			name:            "high reasoning effort",
			reasoningEffort: "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := ToolChatOptions{
				Params: ToolChatParams{
					Messages: []ChatMessage{
						{Role: "user", Content: "test"},
					},
					Tools: []*Tool{},
					ModelConfig: common.ModelConfig{
						Provider:        "openai",
						Model:           "o1",
						ReasoningEffort: tt.reasoningEffort,
					},
				},
			}

			params := options.ActionParams()

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

func TestActionParams_OmitsMaxTokensWhenUnset(t *testing.T) {
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
			Tools: []*Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-latest",
			},
		},
	}

	params := options.ActionParams()

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

func TestActionParams_IncludesMaxTokensWhenSet(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
	}{
		{
			name:      "small max tokens",
			maxTokens: 123,
		},
		{
			name:      "medium max tokens",
			maxTokens: 4000,
		},
		{
			name:      "large max tokens",
			maxTokens: 8000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := ToolChatOptions{
				Params: ToolChatParams{
					Messages: []ChatMessage{
						{Role: "user", Content: "test"},
					},
					Tools: []*Tool{},
					ModelConfig: common.ModelConfig{
						Provider:  "anthropic",
						Model:     "claude-3-5-sonnet-latest",
						MaxTokens: tt.maxTokens,
					},
				},
			}

			params := options.ActionParams()

			maxTokens, exists := params["maxTokens"]
			if !exists {
				t.Errorf("Expected maxTokens key to be present when MaxTokens is %d, but it was absent", tt.maxTokens)
			}
			if maxTokens != tt.maxTokens {
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

func TestActionParams_OmitsServiceTierWhenEmpty(t *testing.T) {
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
			Tools: []*Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
		},
	}

	params := options.ActionParams()

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

func TestActionParams_IncludesServiceTierWhenSet(t *testing.T) {
	tests := []struct {
		name        string
		serviceTier string
	}{
		{
			name:        "default service tier",
			serviceTier: "default",
		},
		{
			name:        "flex service tier",
			serviceTier: "flex",
		},
		{
			name:        "priority service tier",
			serviceTier: "priority",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := ToolChatOptions{
				Params: ToolChatParams{
					Messages: []ChatMessage{
						{Role: "user", Content: "test"},
					},
					Tools: []*Tool{},
					ModelConfig: common.ModelConfig{
						Provider:    "openai",
						Model:       "gpt-4",
						ServiceTier: tt.serviceTier,
					},
				},
			}

			params := options.ActionParams()

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
