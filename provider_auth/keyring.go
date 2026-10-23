package provider_auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/llm"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
	"strings"

	"github.com/zalando/go-keyring"
)

const KeyringService = "sidekick"

// Overridable in tests to avoid OS keyring dependency.
var (
	KeyringSet    = keyring.Set
	KeyringGet    = keyring.Get
	KeyringDelete = keyring.Delete
)

func APIKeySecretName(provider string) (string, error) {
	switch NormalizeProvider(provider) {
	case ProviderAnthropic:
		return llm.AnthropicApiKeySecretName, nil
	case ProviderOpenAI:
		return llm.OpenaiApiKeySecretName, nil
	case ProviderGoogle:
		return llm.GoogleApiKeySecretName, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
	}
}

func OAuthSecretName(provider string) (string, error) {
	switch NormalizeProvider(provider) {
	case ProviderAnthropic:
		return llm.AnthropicOAuthSecretName, nil
	case ProviderOpenAI:
		return openai_oauth.SecretName, nil
	default:
		return "", fmt.Errorf("%w: %q has no OAuth credential", ErrUnsupportedProvider, provider)
	}
}

// StoreSecretForProfiles saves the same secret value under the derived key of
// each given profile.
func StoreSecretForProfiles(profileIds []string, secretName, secretValue string) error {
	for _, profileId := range profileIds {
		derivedName := secret_manager.ProfileSecretName(profileId, secretName)
		if err := KeyringSet(KeyringService, derivedName, secretValue); err != nil {
			return fmt.Errorf("profile %q: %w", profileId, err)
		}
	}
	return nil
}

// PartitionProfilesBySecret splits profiles into those that already hold a
// stored secret and those that don't.
func PartitionProfilesBySecret(profileIds []string, secretName string) (withSecret, withoutSecret []string, err error) {
	for _, profileId := range profileIds {
		derivedName := secret_manager.ProfileSecretName(profileId, secretName)
		value, getErr := KeyringGet(KeyringService, derivedName)
		if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
			return nil, nil, getErr
		}
		if value != "" {
			withSecret = append(withSecret, profileId)
		} else {
			withoutSecret = append(withoutSecret, profileId)
		}
	}
	return withSecret, withoutSecret, nil
}

func SaveAnthropicOAuthCredentials(creds AnthropicOAuthCredentials, profileIds []string) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth credentials: %w", err)
	}

	if err := StoreSecretForProfiles(profileIds, llm.AnthropicOAuthSecretName, string(credsJSON)); err != nil {
		return fmt.Errorf("error storing OAuth credentials in keyring: %w", err)
	}
	return nil
}

func SaveOpenAIOAuthCredentials(creds *openai_oauth.Credentials, profileIds []string) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth credentials: %w", err)
	}

	if err := StoreSecretForProfiles(profileIds, openai_oauth.SecretName, string(credsJSON)); err != nil {
		return fmt.Errorf("error storing OAuth credentials in keyring: %w", err)
	}
	return nil
}

func SaveAPIKey(provider, profileId, apiKey string) error {
	secretName, err := APIKeySecretName(provider)
	if err != nil {
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("%s API key not provided", provider)
	}

	if err := StoreSecretForProfiles([]string{profileId}, secretName, apiKey); err != nil {
		return fmt.Errorf("error storing API key in keyring: %w", err)
	}
	return nil
}

// DeleteOAuthCredential removes a profile's OAuth credential from the keyring.
// A credential that isn't in the keyring is not an error, since it may live in
// another secret source.
func DeleteOAuthCredential(provider, profileId string) error {
	secretName, err := OAuthSecretName(provider)
	if err != nil {
		return err
	}

	err = KeyringDelete(KeyringService, secret_manager.ProfileSecretName(profileId, secretName))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("error deleting OAuth credentials from keyring: %w", err)
	}
	return nil
}
