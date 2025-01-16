package common

import "strings"

type ModelConfig struct {
	// Provider here is the provider name, not the provider type (though they may be the same)
	Provider string `koanf:"provider" json:"provider"`
	Model    string `koanf:"model,omitempty" json:"model,omitempty"`
}

func (c ModelConfig) NormalizedProviderName() string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(c.Provider, " ", "_"), "-", "_"))
}
