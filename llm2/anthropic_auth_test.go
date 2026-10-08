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

func TestAnthropicCredentialsForRequest_ProviderAuthErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		authType   common.ProviderAuthType
		secrets    map[string]string
		wantReason common.ProviderAuthErrorReason
		wantAuth   common.ProviderAuthType
	}{
		{
			name:       "api key missing",
			authType:   common.ProviderAuthTypeAPI,
			secrets:    map[string]string{},
			wantReason: common.ProviderAuthErrorReasonMissingAPIKey,
			wantAuth:   common.ProviderAuthTypeAPI,
		},
		{
			name:       "any with no credentials",
			authType:   common.ProviderAuthTypeAny,
			secrets:    map[string]string{},
			wantReason: common.ProviderAuthErrorReasonMissingAPIKey,
			wantAuth:   common.ProviderAuthTypeAny,
		},
		{
			name:       "subscription without OAuth credential",
			authType:   common.ProviderAuthTypeSubscription,
			secrets:    map[string]string{"CLAUDE_API_KEY": "api-token"},
			wantReason: common.ProviderAuthErrorReasonInvalidOAuth,
			wantAuth:   common.ProviderAuthTypeSubscription,
		},
		{
			name:       "malformed OAuth credential under any",
			authType:   common.ProviderAuthTypeAny,
			secrets:    map[string]string{llm.AnthropicOAuthSecretName: "not-json", "CLAUDE_API_KEY": "api-token"},
			wantReason: common.ProviderAuthErrorReasonInvalidOAuth,
			wantAuth:   common.ProviderAuthTypeAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := anthropicCredentialsForRequest(
				anthropicAuthTestSecretManager{secrets: tt.secrets},
				"claude",
				tt.authType,
			)

			require.Error(t, err)
			details, ok := common.AsProviderAuthError(err)
			require.True(t, ok, "expected provider auth error, got: %v", err)
			assert.Equal(t, common.ProviderAuthErrorDetails{
				Provider:  "claude",
				ProfileId: common.DefaultProfileId,
				AuthType:  tt.wantAuth,
				Reason:    tt.wantReason,
			}, *details)
		})
	}
}
