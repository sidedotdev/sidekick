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

// LocalConfig represents the local configuration file structure
type LocalConfig struct {
	Providers []ModelProviderConfig    `koanf:"providers,omitempty"`
	LLM       map[string][]ModelConfig `koanf:"llm,omitempty"`
	Embedding map[string][]ModelConfig `koanf:"embedding,omitempty"`
}

// getCustomProviderNames returns a slice of custom provider names
func (c LocalConfig) getCustomProviderNames() []string {
	names := make([]string, 0)
	for _, p := range c.Providers {
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

	providerNames := c.getCustomProviderNames()
	if !slices.Contains(providerNames, provider) {
		return fmt.Errorf("invalid provider name: %s", provider)
	}
	return nil
}

// Validate ensures the LocalConfig is valid
func (c LocalConfig) Validate() error {
	// Validate custom providers
	for _, p := range c.Providers {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid custom LLM provider %s: %w", p.Name, err)
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

// LoadSidekickConfig loads the sidekick configuration from the given file path.
// If the config file doesn't exist, returns an empty config.
// The config file is expected to be in YAML format.
func LoadSidekickConfig(configPath string) (LocalConfig, error) {
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

func GetSidekickConfigDir() string {
	configDir := xdg.ConfigHome

	// prefer ".config" when possible (e.g. on macOS), for developer
	// accessibility to edit this file
	for _, dir := range xdg.ConfigDirs {
		if filepath.Base(dir) == ".config" {
			configDir = dir
			break
		}
	}

	return filepath.Join(configDir, "sidekick")
}

func GetSidekickConfigPath() string {
	return filepath.Join(GetSidekickConfigDir(), "config.yml")
}
