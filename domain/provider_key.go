package domain

import (
	"fmt"
	"sidekick/secret_manager"
	"time"
)

// ProviderType represents the supported LLM providers
type ProviderType string

const (
	OpenAIProvider    ProviderType = "openai"
	AnthropicProvider ProviderType = "anthropic"
)

// ProviderKey represents an API key for an LLM provider
type ProviderKey struct {
	Id                string                           `json:"id"`
	Nickname          *string                          `json:"nickname"`          // Optional friendly name for the key
	ProviderType      ProviderType                     `json:"providerType"`      // Type of LLM provider (openai, anthropic)
	SecretManagerType secret_manager.SecretManagerType `json:"secretManagerType"` // Type of secret manager storing the key
	SecretName        string                           `json:"secretName"`        // Name/identifier of the secret in the manager
	Created           time.Time                        `json:"created"`           // Creation timestamp
	Updated           time.Time                        `json:"updated"`           // Last update timestamp
}

// Validate checks if the ProviderKey has valid values
func (pk *ProviderKey) Validate() error {
	if pk.Id == "" {
		return fmt.Errorf("id is required")
	}

	if pk.ProviderType != OpenAIProvider && pk.ProviderType != AnthropicProvider {
		return fmt.Errorf("invalid provider type: %s", pk.ProviderType)
	}

	if pk.SecretName == "" {
		return fmt.Errorf("secret name is required")
	}

	switch pk.SecretManagerType {
	case secret_manager.EnvSecretManagerType,
		secret_manager.KeyringSecretManagerType,
		secret_manager.MockSecretManagerType:
		// Valid types
	default:
		return fmt.Errorf("invalid secret manager type: %s", pk.SecretManagerType)
	}

	return nil
}
