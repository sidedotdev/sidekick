package common

import (
	"fmt"
)

type ModelConfig struct {
	Provider      string `json:"provider"`
	Model         string `json:"model,omitempty"`
	ProviderKeyId string `json:"providerKeyId,omitempty"`
}

func (mc ModelConfig) ToolChatProvider() ToolChatProvider {
	provider, err := StringToToolChatProvider(mc.Provider)
	if err != nil {
		panic(fmt.Sprintf("AI config: failed to convert provider string to ToolChatProvider: %v", err))
	}
	return provider
}
