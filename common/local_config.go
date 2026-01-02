package common

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/adrg/xdg"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
)

// LocalConfig represents the local configuration file structure
type LocalConfig struct {
	Providers          []ModelProviderConfig    `koanf:"providers,omitempty"`
	LLM                map[string][]ModelConfig `koanf:"llm,omitempty"`
	Embedding          map[string][]ModelConfig `koanf:"embedding,omitempty"`
	CommandPermissions CommandPermissionConfig  `koanf:"command_permissions,omitempty"`
	OffHours           OffHoursConfig           `koanf:"off_hours,omitempty"`
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
	if provider == "openai" || provider == "google" {
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
// Supports YAML (.yml, .yaml), TOML (.toml), and JSON (.json) formats.
func LoadSidekickConfig(configPath string) (LocalConfig, error) {
	k := koanf.New(".")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return LocalConfig{}, nil
	}

	parser := GetParserForExtension(configPath)
	if parser == nil {
		return LocalConfig{}, fmt.Errorf("unsupported config file format: %s", configPath)
	}

	if err := k.Load(file.Provider(configPath), parser); err != nil {
		return LocalConfig{}, fmt.Errorf("error loading config: %w", err)
	}

	var config LocalConfig
	if err := k.Unmarshal("", &config); err != nil {
		return LocalConfig{}, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return LocalConfig{}, fmt.Errorf("invalid config: %w", err)
	}

	for i := range config.Providers {
		p := &config.Providers[i]
		if slices.Contains(BuiltinProviders, p.Type) && p.Name == "" {
			p.Name = p.Type
		}
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

// localConfigCandidates defines the config file names in order of precedence
var localConfigCandidates = []string{"config.yml", "config.yaml", "config.toml", "config.json"}

func GetSidekickConfigPath() string {
	configDir := GetSidekickConfigDir()
	result := DiscoverConfigFile(configDir, localConfigCandidates)

	if len(result.AllFound) > 1 {
		log.Warn().
			Strs("found", result.AllFound).
			Str("using", result.ChosenPath).
			Msg("Multiple sidekick config files found; using highest precedence")
	}

	if result.ChosenPath != "" {
		return result.ChosenPath
	}

	// Default to config.yml when no config exists
	return filepath.Join(configDir, "config.yml")
}
