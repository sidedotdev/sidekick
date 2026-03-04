package llm2

import (
	"sidekick/common"
)

// Options holds the LLM request parameters including tools, model configuration, and temperature.
type Options struct {
	Tools             []*common.Tool    `json:"tools,omitempty"`
	ToolChoice        common.ToolChoice `json:"toolChoice"`
	ParallelToolCalls *bool             `json:"parallelToolCalls,omitempty"`
	Temperature       *float32          `json:"temperature,omitempty"`
	MaxTokens         int               `json:"maxTokens,omitempty"`
	common.ModelConfig
}
