package llm2

import (
	"encoding/json"
	"testing"

	"sidekick/common"
)

func TestOptions_JSON_Flat(t *testing.T) {
	t.Parallel()
	data := `{"tools":[{"name":"myTool"}],"toolChoice":{"type":"auto"},"maxTokens":1024,"provider":"anthropic","model":"claude-sonnet-4-5"}`
	var opts Options
	if err := json.Unmarshal([]byte(data), &opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.Tools) != 1 || opts.Tools[0].Name != "myTool" {
		t.Errorf("tools: got %+v", opts.Tools)
	}
	if opts.MaxTokens != 1024 {
		t.Errorf("maxTokens: got %d, want 1024", opts.MaxTokens)
	}
	if opts.ModelConfig.Provider != "anthropic" {
		t.Errorf("provider: got %q, want %q", opts.ModelConfig.Provider, "anthropic")
	}
}

func TestOptions_JSON_RoundTrip(t *testing.T) {
	t.Parallel()
	original := Options{
		Tools: []*common.Tool{{Name: "testTool"}},
		ToolChoice: common.ToolChoice{
			Type: common.ToolChoiceTypeAuto,
		},
		MaxTokens: 2048,
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4",
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded Options
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.MaxTokens != original.MaxTokens {
		t.Errorf("maxTokens: got %d, want %d", decoded.MaxTokens, original.MaxTokens)
	}
	if decoded.ModelConfig.Provider != original.ModelConfig.Provider {
		t.Errorf("provider: got %q, want %q", decoded.ModelConfig.Provider, original.ModelConfig.Provider)
	}
}
