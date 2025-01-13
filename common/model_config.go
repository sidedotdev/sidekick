package common

import (
	"fmt"
)

type ModelConfig struct {
	// the provider name
	Provider string `koanf:"provider" json:"provider"`
	Model    string `koanf:"model,omitempty" json:"model,omitempty"`
}

func (mc ModelConfig) ToolChatProvider() ToolChatProviderType {
	// FIXME this treating the provider name as if it is the provider type. won't work for custom providers.
	provider, err := StringToToolChatProviderType(mc.Provider)
	if err != nil {
		panic(fmt.Sprintf("AI config: failed to convert provider string to ToolChatProvider: %v", err))
	}
	return provider
}
