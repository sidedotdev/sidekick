package common

import (
	"fmt"
)

// LocalPublicConfig represents the local configuration without keys
type LocalPublicConfig struct {
	Providers []ModelProviderPublicConfig `json:"providers,omitempty"`
	LLM       LLMConfig                   `json:"llm"`
	Embedding EmbeddingConfig             `json:"embedding"`
}

// ModelProviderPublicConfig represents the model provider configuration without keys
type ModelProviderPublicConfig struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	BaseURL    string `json:"base_url,omitempty"`
	DefaultLLM string `json:"default_llm,omitempty"`
	SmallLLM   string `json:"small_llm,omitempty"`
}

// GetLocalConfig loads the local configuration and converts it to a format
// suitable for client consumption, with sensitive data removed
func GetLocalConfig() (LocalPublicConfig, error) {
	config, err := LoadSidekickConfig(GetSidekickConfigPath())
	if err != nil {
		return LocalPublicConfig{}, fmt.Errorf("failed to load config: %w", err)
	}

	// Strip sensitive data from provider configs
	providers := make([]ModelProviderPublicConfig, len(config.Providers))
	for i, p := range config.Providers {
		// Create copy without sensitive key
		providers[i] = ModelProviderPublicConfig{
			Name:       p.Name,
			Type:       p.Type,
			BaseURL:    p.BaseURL,
			DefaultLLM: p.DefaultLLM,
			SmallLLM:   p.SmallLLM,
		}
	}

	// Convert map-based LLM config to structured format
	llmConfig := LLMConfig{
		UseCaseConfigs: make(map[string][]ModelConfig),
	}
	if defaults, ok := config.LLM[DefaultKey]; ok {
		llmConfig.Defaults = defaults
	} else if defaults, ok := config.LLM["defaults"]; ok {
		llmConfig.Defaults = defaults
	}
	for key, models := range config.LLM {
		if key != DefaultKey {
			llmConfig.UseCaseConfigs[key] = models
		}
	}

	// Convert map-based Embedding config to structured format
	embeddingConfig := EmbeddingConfig{
		UseCaseConfigs: make(map[string][]ModelConfig),
	}
	if defaults, ok := config.Embedding[DefaultKey]; ok {
		embeddingConfig.Defaults = defaults
	} else if defaults, ok := config.Embedding["defaults"]; ok {
		embeddingConfig.Defaults = defaults
	}
	for key, models := range config.Embedding {
		if key != DefaultKey {
			embeddingConfig.UseCaseConfigs[key] = models
		}
	}

	// Verify that at least one config has defaults
	if len(llmConfig.Defaults) == 0 && len(embeddingConfig.Defaults) == 0 {
		return LocalPublicConfig{}, fmt.Errorf("no default models configured in local config")
	}

	return LocalPublicConfig{
		Providers: providers,
		LLM:       llmConfig,
		Embedding: embeddingConfig,
	}, nil
}
