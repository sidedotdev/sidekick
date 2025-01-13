package common

import (
	"fmt"
)

// LocalConfigResponse represents the local configuration without sensitive data
type LocalConfigResponse struct {
	CustomProviders []ModelProviderConfig `json:"custom_providers,omitempty"`
	LLM             LLMConfig             `json:"llm"`
	Embedding       EmbeddingConfig       `json:"embedding"`
}

// GetLocalConfig loads the local configuration and converts it to a format
// suitable for client consumption, with sensitive data removed
func GetLocalConfig(configPath string) (LocalConfigResponse, error) {
	config, err := LoadSidekickConfig(configPath)
	if err != nil {
		return LocalConfigResponse{}, fmt.Errorf("failed to load config: %w", err)
	}

	// Strip sensitive data from provider configs
	providers := make([]ModelProviderConfig, len(config.CustomProviders))
	for i, p := range config.CustomProviders {
		// Create copy without sensitive key
		providers[i] = ModelProviderConfig{
			Name:         p.Name,
			ProviderType: p.ProviderType,
			BaseURL:      p.BaseURL,
			DefaultLLM:   p.DefaultLLM,
			SmallLLM:     p.SmallLLM,
		}
	}

	// Convert map-based LLM config to structured format
	llmConfig := LLMConfig{
		UseCaseConfigs: make(map[string][]ModelConfig),
	}
	if defaults, ok := config.LLM[DefaultKey]; ok {
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
	}
	for key, models := range config.Embedding {
		if key != DefaultKey {
			embeddingConfig.UseCaseConfigs[key] = models
		}
	}

	// Verify that at least one config has defaults
	if len(llmConfig.Defaults) == 0 && len(embeddingConfig.Defaults) == 0 {
		return LocalConfigResponse{}, fmt.Errorf("no default models configured in local config")
	}

	return LocalConfigResponse{
		CustomProviders: providers,
		LLM:             llmConfig,
		Embedding:       embeddingConfig,
	}, nil
}
