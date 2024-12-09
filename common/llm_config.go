package common

import (
	"fmt"
	"sidekick/llm"
)

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

func (c LLMConfig) GetToolChatConfig(key string, iteration int) (llm.ToolChatProvider, ModelConfig, bool) {
	modelConfigs, isDefault := c.GetModelsOrDefault(key)
	if len(modelConfigs) == 0 {
		panic("LLM config: no default model config found")
	}

	// TODO add logic for MaxContextChars + param for context size, to choose
	// the correct modelConfig from the list of configs based on which can fit
	// the context size
	modelConfig := modelConfigs[iteration%len(modelConfigs)]
	provider, err := llm.StringToToolChatProvider(modelConfig.Provider)
	if err != nil {
		panic(fmt.Sprintf("AI config: failed to convert provider string to ToolChatProvider: %v", err))
	} else if provider == llm.UnspecifiedToolChatProvider {
		panic("AI config: provider is empty")
	} else if modelConfig.Model == "" && !isDefault {
		panic("AI config: model is empty")
	}

	return provider, modelConfig, isDefault
}
