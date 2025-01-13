package common

import (
	"fmt"
	"slices"
)

// ValidProviderTypes are the allowed provider types for custom providers
var ValidProviderTypes = []string{"openai", "anthropic", "openai_compatible"}

// BuiltinProviders are the providers that are built into the system
var BuiltinProviders = []string{"openai", "anthropic"}

// ModelProviderConfig represents configuration for an LLM or embedding provider
type ModelProviderConfig struct {
	Name       string `koanf:"name" json:"name"`
	Type       string `koanf:"type" json:"type"`
	BaseURL    string `koanf:"base_url,omitempty" json:"base_url,omitempty"`
	Key        string `koanf:"key" json:"key"`
	DefaultLLM string `koanf:"default_llm,omitempty" json:"default_llm,omitempty"`
	SmallLLM   string `koanf:"small_llm,omitempty" json:"small_llm,omitempty"`
}

// Validate ensures the CustomProviderConfig is valid
func (c ModelProviderConfig) Validate() error {
	if c.Type == "" {
		return fmt.Errorf("provider type is required")
	}
	if !slices.Contains(ValidProviderTypes, c.Type) {
		return fmt.Errorf("invalid provider type: %s", c.Type)
	}
	if c.Name == "" && !slices.Contains(BuiltinProviders, c.Type) {
		return fmt.Errorf("name is required for custom provider types like openai_compatible")
	}
	if c.Key == "" {
		return fmt.Errorf("key is required")
	}
	return nil
}
