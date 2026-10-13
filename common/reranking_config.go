package common

const DefaultCohereRerankingModel = "rerank-v4.0-pro"

type RerankingConfig struct {
	Defaults       []ModelConfig            `json:"defaults"`
	UseCaseConfigs map[string][]ModelConfig `json:"useCaseConfigs"`
}

func DefaultRerankingModelConfig() ModelConfig {
	return ModelConfig{Provider: "cohere", Model: DefaultCohereRerankingModel}
}

func (c RerankingConfig) GetModelConfig(key string) ModelConfig {
	models := c.Defaults
	if key != DefaultKey {
		if selected, ok := c.UseCaseConfigs[key]; ok && selected != nil {
			models = selected
		}
	}
	if models == nil {
		return DefaultRerankingModelConfig()
	}
	if len(models) == 0 {
		return ModelConfig{}
	}
	return models[0]
}

func RerankingConfigForProfile(config RerankingConfig, providers []ModelProviderPublicConfig, profileId string) RerankingConfig {
	// Materialize the implicit selection before filtering so an unavailable
	// provider cannot turn back into an implicit default.
	if config.Defaults == nil {
		config.Defaults = []ModelConfig{DefaultRerankingModelConfig()}
	}
	return RerankingConfig{
		Defaults:       ModelsForProfile(config.Defaults, providers, profileId),
		UseCaseConfigs: useCaseConfigsForProfile(config.UseCaseConfigs, providers, profileId),
	}
}
