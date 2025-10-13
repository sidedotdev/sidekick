package llm2

import (
	"sidekick/common"
	"sidekick/secret_manager"
)

// Params holds the LLM request parameters including messages, tools, and model configuration.
type Params struct {
	Messages          []Message
	Tools             []*common.Tool
	ToolChoice        common.ToolChoice
	ParallelToolCalls *bool
	Temperature       *float32
	common.ModelConfig
}

// Options combines request parameters with secrets for provider authentication.
type Options struct {
	Params  Params
	Secrets secret_manager.SecretManagerContainer
}

// ActionParams returns a map of action parameters suitable for logging or workflow metadata.
func (o Options) ActionParams() map[string]any {
	return map[string]any{
		"messages":          o.Params.Messages,
		"tools":             o.Params.Tools,
		"toolChoice":        o.Params.ToolChoice,
		"model":             o.Params.Model,
		"reasoningEffort":   o.Params.ReasoningEffort,
		"provider":          o.Params.Provider,
		"temperature":       o.Params.Temperature,
		"parallelToolCalls": o.Params.ParallelToolCalls,
	}
}
