package llm2

import (
	"sidekick/common"
	"sidekick/secret_manager"
)

// Params holds the LLM request parameters including tools, model configuration, and chat history.
type Params struct {
	ChatHistory       *ChatHistoryContainer
	Tools             []*common.Tool
	ToolChoice        common.ToolChoice
	ParallelToolCalls *bool
	Temperature       *float32
	MaxTokens         int `json:"maxTokens,omitempty"`
	common.ModelConfig
}

// Options combines request parameters with secrets for provider authentication.
type Options struct {
	Params  Params
	Secrets secret_manager.SecretManagerContainer
}

// ActionParams returns a map of action parameters suitable for logging or workflow metadata.
func (o Options) ActionParams() map[string]any {
	params := map[string]any{
		"messages":          o.Params.ChatHistory,
		"tools":             o.Params.Tools,
		"toolChoice":        o.Params.ToolChoice,
		"model":             o.Params.Model,
		"provider":          o.Params.Provider,
		"temperature":       o.Params.Temperature,
		"parallelToolCalls": o.Params.ParallelToolCalls,
	}
	if o.Params.ReasoningEffort != "" {
		params["reasoningEffort"] = o.Params.ReasoningEffort
	}
	if o.Params.MaxTokens > 0 {
		params["maxTokens"] = o.Params.MaxTokens
	}
	if o.Params.ServiceTier != "" {
		params["serviceTier"] = o.Params.ServiceTier
	}
	return params
}
