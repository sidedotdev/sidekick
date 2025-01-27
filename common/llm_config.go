package common

const (
	DefaultKey          = "default"
	PlanningKey         = "planning"
	CodingKey           = "coding"
	CodeLocalizationKey = "code_localization"
	JudgingKey          = "judging"
	SummarizationKey    = "summarization"
	QueryExpansionKey   = "query_expansion"
)

type LLMConfig struct {
	Defaults       []ModelConfig            `json:"defaults"`
	UseCaseConfigs map[string][]ModelConfig `json:"useCaseConfigs"`
}

// GetModels returns the models for a specific use case in LLM configuration.
func (c LLMConfig) GetModels(key string) []ModelConfig {
	if key == DefaultKey {
		return c.Defaults
	}

	if models, ok := c.UseCaseConfigs[key]; ok {
		return models
	}
	return nil
}

// GetModelsOrDefault returns the models for a specific use case or the default
// models if the use case is not found.
func (c LLMConfig) GetModelsOrDefault(key string) ([]ModelConfig, bool) {
	if models := c.GetModels(key); models != nil {
		isDefault := key == DefaultKey
		return models, isDefault
	}
	return c.Defaults, true
}

func (c LLMConfig) GetModelConfig(key string, iteration int) (ModelConfig, bool) {
	modelConfigs, isDefault := c.GetModelsOrDefault(key)
	if len(modelConfigs) == 0 {
		panic("LLM config: no default model config found")
	}

	modelConfig := modelConfigs[iteration%len(modelConfigs)]

	return modelConfig, isDefault
}
