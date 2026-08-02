package main

import (
	"sidekick/llm"
	"sidekick/openai_oauth"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zalando/go-keyring"
)

func newMockKeyringGet(secrets map[string]string) func(string, string) (string, error) {
	return func(service, key string) (string, error) {
		if val, ok := secrets[key]; ok && val != "" {
			return val, nil
		}
		return "", keyring.ErrNotFound
	}
}

func TestGetConfiguredBuiltinLLMProviders(t *testing.T) {

	tests := []struct {
		name     string
		secrets  map[string]string
		expected []string
	}{
		{
			name:     "no secrets configured",
			secrets:  map[string]string{},
			expected: nil,
		},
		{
			name: "openai API key only",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName: "sk-test-key",
			},
			expected: []string{"openai"},
		},
		{
			name: "openai OAuth only",
			secrets: map[string]string{
				openai_oauth.SecretName: `{"access_token":"tok","refresh_token":"ref","expires_at":9999999999,"account_id":"acc"}`,
			},
			expected: []string{"openai"},
		},
		{
			name: "openai both API key and OAuth",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName: "sk-test-key",
				openai_oauth.SecretName:    `{"access_token":"tok","refresh_token":"ref","expires_at":9999999999,"account_id":"acc"}`,
			},
			expected: []string{"openai"},
		},
		{
			name: "google API key only",
			secrets: map[string]string{
				llm.GoogleApiKeySecretName: "google-key",
			},
			expected: []string{"google"},
		},
		{
			name: "anthropic API key only",
			secrets: map[string]string{
				llm.AnthropicApiKeySecretName: "anthro-key",
			},
			expected: []string{"anthropic"},
		},
		{
			name: "anthropic OAuth only",
			secrets: map[string]string{
				AnthropicOAuthSecretName: `{"access_token":"tok","refresh_token":"ref","expires_at":9999999999}`,
			},
			expected: []string{"anthropic"},
		},
		{
			name: "all providers configured",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName:    "sk-test-key",
				llm.GoogleApiKeySecretName:    "google-key",
				llm.AnthropicApiKeySecretName: "anthro-key",
			},
			expected: []string{"openai", "google", "anthropic"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := keyringGet
			keyringGet = newMockKeyringGet(tc.secrets)
			t.Cleanup(func() { keyringGet = orig })

			result := getConfiguredBuiltinLLMProviders()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetConfiguredBuiltinEmbeddingProviders(t *testing.T) {
	tests := []struct {
		name     string
		secrets  map[string]string
		expected []string
	}{
		{
			name:     "no secrets configured",
			secrets:  map[string]string{},
			expected: nil,
		},
		{
			name: "openai API key only",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName: "sk-test-key",
			},
			expected: []string{"openai"},
		},
		{
			name: "openai OAuth only - not eligible for embeddings",
			secrets: map[string]string{
				openai_oauth.SecretName: `{"access_token":"tok","refresh_token":"ref","expires_at":9999999999,"account_id":"acc"}`,
			},
			expected: nil,
		},
		{
			name: "openai both API key and OAuth - embeddings use API key",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName: "sk-test-key",
				openai_oauth.SecretName:    `{"access_token":"tok","refresh_token":"ref","expires_at":9999999999,"account_id":"acc"}`,
			},
			expected: []string{"openai"},
		},
		{
			name: "google API key only",
			secrets: map[string]string{
				llm.GoogleApiKeySecretName: "google-key",
			},
			expected: []string{"google"},
		},
		{
			name: "all API keys configured",
			secrets: map[string]string{
				llm.OpenaiApiKeySecretName: "sk-test-key",
				llm.GoogleApiKeySecretName: "google-key",
			},
			expected: []string{"openai", "google"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := keyringGet
			keyringGet = newMockKeyringGet(tc.secrets)
			t.Cleanup(func() { keyringGet = orig })

			result := getConfiguredBuiltinEmbeddingProviders()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetConfiguredBuiltinLLMProviders_NoDuplicates(t *testing.T) {

	orig := keyringGet
	keyringGet = newMockKeyringGet(map[string]string{
		llm.OpenaiApiKeySecretName: "sk-test-key",
		openai_oauth.SecretName:    `{"access_token":"tok"}`,
	})
	t.Cleanup(func() { keyringGet = orig })

	result := getConfiguredBuiltinLLMProviders()
	count := 0
	for _, p := range result {
		if p == "openai" {
			count++
		}
	}
	assert.Equal(t, 1, count, "openai should appear exactly once even when both API key and OAuth are configured")
}
