package llm2

import (
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type anthropicAuthTestSecretManager struct {
	secrets map[string]string
}

func (m anthropicAuthTestSecretManager) GetSecret(secretName string) (string, error) {
	if secret, ok := m.secrets[secretName]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (m anthropicAuthTestSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

func TestAnthropicCredentialsForRequest(t *testing.T) {
	t.Parallel()

	oauthJSON, err := json.Marshal(llm.OAuthCredentials{
		AccessToken:  "oauth-access-token",
		RefreshToken: "oauth-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	t.Run("AnyPrefersOAuth", func(t *testing.T) {
		t.Parallel()

		oauthCreds, token, useOAuth, err := anthropicCredentialsForRequest(
			anthropicAuthTestSecretManager{
				secrets: map[string]string{
					llm.AnthropicOAuthSecretName: string(oauthJSON),
					"ANTHROPIC_API_KEY":          "api-token",
				},
			},
			"ANTHROPIC",
			common.ProviderAuthTypeAny,
		)

		require.NoError(t, err)
		assert.True(t, useOAuth)
		require.NotNil(t, oauthCreds)
		assert.Equal(t, "oauth-access-token", oauthCreds.AccessToken)
		assert.Empty(t, token)
	})

	t.Run("AnyFallsBackToAPIKey", func(t *testing.T) {
		t.Parallel()

		oauthCreds, token, useOAuth, err := anthropicCredentialsForRequest(
			anthropicAuthTestSecretManager{
				secrets: map[string]string{
					"ANTHROPIC_API_KEY": "api-token",
				},
			},
			"ANTHROPIC",
			common.ProviderAuthTypeAny,
		)

		require.NoError(t, err)
		assert.False(t, useOAuth)
		assert.Nil(t, oauthCreds)
		assert.Equal(t, "api-token", token)
	})

	t.Run("APIBypassesOAuth", func(t *testing.T) {
		t.Parallel()

		oauthCreds, token, useOAuth, err := anthropicCredentialsForRequest(
			anthropicAuthTestSecretManager{
				secrets: map[string]string{
					llm.AnthropicOAuthSecretName: string(oauthJSON),
					"ANTHROPIC_API_KEY":          "api-token",
				},
			},
			"ANTHROPIC",
			common.ProviderAuthTypeAPI,
		)

		require.NoError(t, err)
		assert.False(t, useOAuth)
		assert.Nil(t, oauthCreds)
		assert.Equal(t, "api-token", token)
	})

	t.Run("SubscriptionRequiresOAuth", func(t *testing.T) {
		t.Parallel()

		oauthCreds, token, useOAuth, err := anthropicCredentialsForRequest(
			anthropicAuthTestSecretManager{
				secrets: map[string]string{
					"ANTHROPIC_API_KEY": "api-token",
				},
			},
			"ANTHROPIC",
			common.ProviderAuthTypeSubscription,
		)

		require.Error(t, err)
		assert.False(t, useOAuth)
		assert.Nil(t, oauthCreds)
		assert.Empty(t, token)
		assert.Contains(t, err.Error(), "requires OAuth credentials")
	})
}
