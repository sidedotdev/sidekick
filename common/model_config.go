package common

import (
	"fmt"
)

type ModelConfig struct {
	Provider string `koanf:"provider"`
	Model    string `koanf:"model,omitempty"`
}

func (mc ModelConfig) ToolChatProvider() ToolChatProvider {
	provider, err := StringToToolChatProvider(mc.Provider)
	if err != nil {
		panic(fmt.Sprintf("AI config: failed to convert provider string to ToolChatProvider: %v", err))
	}
	return provider
}
