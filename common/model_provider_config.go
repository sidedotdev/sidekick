package common

import (
	"fmt"
	"slices"
)

// ValidProviderTypes are the allowed provider types for custom providers
var ValidProviderTypes = []string{"openai", "anthropic", "openai_compatible", "google", "openai_responses_compatible", "bedrock"}

// BuiltinProviders are the providers that are built into the system
var BuiltinProviders = []string{"openai", "anthropic", "google", "bedrock"}

// ModelProviderConfig represents configuration for an LLM or embedding provider
type ModelProviderConfig struct {
	Name          string            `koanf:"name" json:"name"`
	Type          string            `koanf:"type" json:"type"`
	BaseURL       string            `koanf:"base_url,omitempty" json:"base_url,omitempty"`
	Key           string            `koanf:"key" json:"key"`
	DefaultLLM    string            `koanf:"default_llm,omitempty" json:"default_llm,omitempty"`
	SmallLLM      string            `koanf:"small_llm,omitempty" json:"small_llm,omitempty"`
	AuthType      ProviderAuthType  `koanf:"auth_type,omitempty" json:"auth_type,omitempty"`
	CustomHeaders map[string]string `koanf:"custom_headers,omitempty" json:"custom_headers,omitempty"`

	// Profiles associates the provider with a set of profiles. A non-configured
	// (nil) association means the default profile, while an explicitly empty
	// list associates the provider with no profile at all.
	Profiles *[]string `koanf:"profiles,omitempty" json:"profiles,omitempty"`
}

// EffectiveProfiles returns the profile ids this provider is associated with.
func (c ModelProviderConfig) EffectiveProfiles() []string {
	return EffectiveProfileIds(c.Profiles)
}

// MatchesProfile reports whether this provider is associated with the given
// profile, where an empty profile id means the default profile.
func (c ModelProviderConfig) MatchesProfile(profileId string) bool {
	return MatchesProfile(c.Profiles, profileId)
}

// Validate ensures the CustomProviderConfig is valid
func (c ModelProviderConfig) Validate() error {
	if c.Type == "" {
		return fmt.Errorf("provider type is required")
	}
	if !slices.Contains(ValidProviderTypes, c.Type) {
		return fmt.Errorf("invalid provider type: %s", c.Type)
	}
	if c.Name == "" && !slices.Contains(BuiltinProviders, c.Type) {
		return fmt.Errorf("name is required for custom provider types like openai_compatible")
	}
	if err := ValidateProviderAuthType(string(c.AuthType)); err != nil {
		return err
	}
	if c.Key == "" && c.AuthType == ProviderAuthTypeAPI {
		return fmt.Errorf("key is required for auth type %s", c.AuthType)
	}
	if c.Profiles != nil {
		for _, profileId := range *c.Profiles {
			if profileId == "" {
				return fmt.Errorf("profile id is required for each associated profile")
			}
		}
	}
	return nil
}
