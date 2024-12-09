package common

import (
	"fmt"
	"sidekick/llm"
)

type ModelConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

func (mc ModelConfig) ToolChatProvider() llm.ToolChatProvider {
	provider, err := llm.StringToToolChatProvider(mc.Provider)
	if err != nil {
		panic(fmt.Sprintf("AI config: failed to convert provider string to ToolChatProvider: %v", err))
	}
	return provider
}
