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
