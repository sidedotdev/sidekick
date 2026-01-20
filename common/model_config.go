package common

import "strings"

type ModelConfig struct {
	// Provider here is the provider name, not the provider type (though they may be the same)
	Provider string `koanf:"provider" json:"provider"`
	Model    string `koanf:"model,omitempty" json:"model,omitempty"`
	// Optional hint for models that support dedicated reasoning modes.
	// Allowed values: minimal | low | medium | high
	ReasoningEffort string `koanf:"reasoning_effort" json:"reasoningEffort,omitempty"`
	// Optional maximum number of tokens to generate. Values <= 0 are treated as unset.
	MaxTokens int `koanf:"max_tokens" json:"maxTokens,omitempty"`
	// Optional extra body parameters to pass to the provider API.
	ExtraBody map[string]any `koanf:"extra_body" json:"extraBody,omitempty"`
}

func (c ModelConfig) NormalizedProviderName() string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(c.Provider, " ", "_"), "-", "_"))
}
