package common

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/adrg/xdg"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// ValidProviderTypes are the allowed provider types for custom providers
var ValidProviderTypes = []string{"openai", "anthropic", "openai_compatible"}

// BuiltinProviders are the providers that are built into the system
var BuiltinProviders = []string{"openai", "anthropic"}

// CustomProviderConfig represents configuration for a custom LLM or embedding provider
type CustomProviderConfig struct {
	Name         string `koanf:"name"`
	ProviderType string `koanf:"provider_type"`
	BaseURL      string `koanf:"base_url"`
	Key          string `koanf:"key"`
	DefaultModel string `koanf:"default_model,omitempty"`
}

// Validate ensures the CustomProviderConfig is valid
func (c CustomProviderConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.ProviderType == "" {
		return fmt.Errorf("provider_type is required")
	}
	if !slices.Contains(ValidProviderTypes, c.ProviderType) {
		return fmt.Errorf("invalid provider_type: %s", c.ProviderType)
	}
	if c.BaseURL == "" {
		return fmt.Errorf("base_url is required")
	}
	if c.Key == "" {
		return fmt.Errorf("key is required")
	}
	return nil
}

// LocalConfig represents the local configuration file structure
type LocalConfig struct {
	CustomLLMProviders       []CustomProviderConfig   `koanf:"custom_llm_providers,omitempty"`
	CustomEmbeddingProviders []CustomProviderConfig   `koanf:"custom_embedding_providers,omitempty"`
	LLM                      map[string][]ModelConfig `koanf:"llm,omitempty"`
	Embedding                map[string][]ModelConfig `koanf:"embedding,omitempty"`
}

// getCustomProviderNames returns a slice of custom provider names
func (c LocalConfig) getCustomProviderNames() []string {
	names := make([]string, 0)
	for _, p := range c.CustomLLMProviders {
		names = append(names, p.Name)
	}
	for _, p := range c.CustomEmbeddingProviders {
		names = append(names, p.Name)
	}
	return names
}

// validateProvider checks if a provider name is valid
func (c LocalConfig) validateProvider(provider string, allowAnthropicProvider bool) error {
	if provider == "openai" {
		return nil
	}
	if provider == "anthropic" {
		if !allowAnthropicProvider {
			return fmt.Errorf("anthropic provider is not allowed for embeddings")
		}
		return nil
	}

	customProviders := c.getCustomProviderNames()
	if !slices.Contains(customProviders, provider) {
		return fmt.Errorf("invalid provider: %s", provider)
	}
	return nil
}

// Validate ensures the LocalConfig is valid
func (c LocalConfig) Validate() error {
	// Validate custom providers
	for _, p := range c.CustomLLMProviders {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid custom LLM provider %s: %w", p.Name, err)
		}
	}
	for _, p := range c.CustomEmbeddingProviders {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid custom embedding provider %s: %w", p.Name, err)
		}
	}

	// Validate LLM configs
	for useCase, configs := range c.LLM {
		for _, mc := range configs {
			if err := c.validateProvider(mc.Provider, true); err != nil {
				return fmt.Errorf("invalid provider in LLM config for use case %s: %w", useCase, err)
			}
		}
	}

	// Validate Embedding configs
	for useCase, configs := range c.Embedding {
		for _, mc := range configs {
			if err := c.validateProvider(mc.Provider, false); err != nil {
				return fmt.Errorf("invalid provider in Embedding config for use case %s: %w", useCase, err)
			}
		}
	}

	return nil
}

// GetSidekickConfig loads the sidekick configuration from the given file path.
// If the config file doesn't exist, returns an empty config.
// The config file is expected to be in YAML format.
func GetSidekickConfig(configPath string) (LocalConfig, error) {
	k := koanf.New(".")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return LocalConfig{}, nil
	}

	// Load YAML config
	if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
		return LocalConfig{}, fmt.Errorf("error loading config: %w", err)
	}

	var config LocalConfig
	if err := k.Unmarshal("", &config); err != nil {
		return LocalConfig{}, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return LocalConfig{}, fmt.Errorf("invalid config: %w", err)
	}

	return config, nil
}

// GetDefaultConfigPath returns the default path for the sidekick config file
func GetDefaultConfigPath() string {
	return filepath.Join(xdg.ConfigHome, "sidekick", "config.yaml")
}
