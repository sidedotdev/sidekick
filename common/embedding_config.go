package common

type EmbeddingConfig struct {
	Defaults       []ModelConfig            `json:"defaults"`
	UseCaseConfigs map[string][]ModelConfig `json:"useCaseConfigs"`
}

// GetModels returns the models for a specific use case in Embedding configuration.
func (c EmbeddingConfig) GetModels(key string) []ModelConfig {
	if key == DefaultKey {
		return c.Defaults
	}

	if models, ok := c.UseCaseConfigs[key]; ok {
		return models
	}
	return nil
}

// GetModelsOrDefault returns the models for a specific use case or the default
// models if use-case-specific config is not found.
func (c EmbeddingConfig) GetModelsOrDefault(key string) []ModelConfig {
	if models := c.GetModels(key); models != nil {
		return models
	}
	return c.Defaults
}

func (c EmbeddingConfig) GetModelConfig(key string) ModelConfig {
	modelConfigs := c.GetModelsOrDefault(key)
	if len(modelConfigs) == 0 {
		panic("Embedding config: no default model config found")
	}

	modelConfig := modelConfigs[0]
	return modelConfig
}