package common

import (
	"fmt"
	"os"

	"go.temporal.io/sdk/temporal"
)

// LocalPublicConfig represents the local configuration without keys
type LocalPublicConfig struct {
	Providers          []ModelProviderPublicConfig `json:"providers,omitempty"`
	Profiles           []Profile                   `json:"profiles,omitempty"`
	LLM                LLMConfig                   `json:"llm"`
	Embedding          EmbeddingConfig             `json:"embedding"`
	CommandPermissions CommandPermissionConfig     `json:"commandPermissions,omitempty"`
}

// ModelProviderPublicConfig represents the model provider configuration without keys
type ModelProviderPublicConfig struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	BaseURL       string            `json:"base_url,omitempty"`
	DefaultLLM    string            `json:"default_llm,omitempty"`
	SmallLLM      string            `json:"small_llm,omitempty"`
	AuthType      ProviderAuthType  `json:"auth_type,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
	BuiltinTools  []string          `json:"builtin_tools,omitempty"`

	// Profiles mirrors ModelProviderConfig.Profiles: nil means the default
	// profile, while an explicitly empty list means no profile at all.
	Profiles *[]string `json:"profiles,omitempty"`
}

// EffectiveProfiles returns the profile ids this provider is associated with.
func (c ModelProviderPublicConfig) EffectiveProfiles() []string {
	return EffectiveProfileIds(c.Profiles)
}

// MatchesProfile reports whether this provider is associated with the given
// profile, where an empty profile id means the default profile.
func (c ModelProviderPublicConfig) MatchesProfile(profileId string) bool {
	return MatchesProfile(c.Profiles, profileId)
}

// GetLocalConfig loads the local configuration and converts it to a format
// suitable for client consumption, with sensitive data removed
func GetLocalConfig() (LocalPublicConfig, error) {
	configPath := GetSidekickConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return LocalPublicConfig{}, temporal.NewNonRetryableApplicationError(
			"failed to load config: not found",
			"LocalConfigNotFound",
			nil,
		)
	}

	config, err := LoadSidekickConfig(configPath)
	if err != nil {
		return LocalPublicConfig{}, fmt.Errorf("failed to load config: %w", err)
	}

	return localPublicConfigFrom(config)
}

// localPublicConfigFrom converts a loaded local config into the public form
// consumed by workflows and clients, with sensitive data removed.
func localPublicConfigFrom(config LocalConfig) (LocalPublicConfig, error) {
	providers := make([]ModelProviderPublicConfig, len(config.Providers))
	for i, p := range config.Providers {
		// Create copy without sensitive key
		providers[i] = ModelProviderPublicConfig{
			Name:          p.Name,
			Type:          p.Type,
			BaseURL:       p.BaseURL,
			DefaultLLM:    p.DefaultLLM,
			SmallLLM:      p.SmallLLM,
			AuthType:      NormalizeProviderAuthType(string(p.AuthType)),
			CustomHeaders: p.CustomHeaders,
			BuiltinTools:  p.BuiltinTools,
			Profiles:      p.Profiles,
		}
	}

	// Convert map-based LLM config to structured format
	llmConfig := LLMConfig{
		UseCaseConfigs: make(map[string][]ModelConfig),
	}
	if defaults, ok := config.LLM[DefaultKey]; ok {
		llmConfig.Defaults = defaults
	} else if defaults, ok := config.LLM["defaults"]; ok {
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
	} else if defaults, ok := config.Embedding["defaults"]; ok {
		embeddingConfig.Defaults = defaults
	}
	for key, models := range config.Embedding {
		if key != DefaultKey {
			embeddingConfig.UseCaseConfigs[key] = models
		}
	}

	// Verify that at least one config has defaults
	if len(llmConfig.Defaults) == 0 && len(embeddingConfig.Defaults) == 0 {
		return LocalPublicConfig{}, temporal.NewNonRetryableApplicationError(
			"no default models configured in local config",
			"LocalConfigNoDefaults",
			nil,
		)
	}

	return LocalPublicConfig{
		Providers:          providers,
		Profiles:           config.ResolveProfiles(),
		LLM:                llmConfig,
		Embedding:          embeddingConfig,
		CommandPermissions: config.CommandPermissions,
	}, nil
}
