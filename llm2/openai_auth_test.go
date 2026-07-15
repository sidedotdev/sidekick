package llm2

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type openAIAuthTestSecretManager struct {
	secrets map[string]string
	errs    map[string]error
	calls   []string
}

func (m *openAIAuthTestSecretManager) GetSecret(secretName string) (string, error) {
	m.calls = append(m.calls, secretName)
	if err := m.errs[secretName]; err != nil {
		return "", err
	}
	if secret, ok := m.secrets[secretName]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (m *openAIAuthTestSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

func TestOpenAICredentialsForRequest(t *testing.T) {
	oauthJSON, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "oauth-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    9999999999,
		AccountID:    "account-id",
	})
	require.NoError(t, err)

	tests := []struct {
		name              string
		authType          common.ProviderAuthType
		secrets           map[string]string
		errs              map[string]error
		want              openAIRequestCredentials
		wantCalls         []string
		wantErrorContains string
	}{
		{
			name:     "any prefers OAuth",
			authType: common.ProviderAuthTypeAny,
			secrets: map[string]string{
				"OPENAI_OAUTH":   string(oauthJSON),
				"OPENAI_API_KEY": "api-key",
			},
			want: openAIRequestCredentials{
				token:     "oauth-token",
				accountID: "account-id",
				useOAuth:  true,
				authType:  common.ProviderAuthTypeSubscription,
			},
			wantCalls: []string{"OPENAI_OAUTH"},
		},
		{
			name:     "any falls back when OAuth is missing",
			authType: common.ProviderAuthTypeAny,
			secrets: map[string]string{
				"OPENAI_API_KEY": "api-key",
			},
			want: openAIRequestCredentials{
				token:    "api-key",
				authType: common.ProviderAuthTypeAPI,
			},
			wantCalls: []string{"OPENAI_OAUTH", "OPENAI_API_KEY"},
		},
		{
			name:     "api does not request OAuth",
			authType: common.ProviderAuthTypeAPI,
			secrets: map[string]string{
				"OPENAI_OAUTH":   string(oauthJSON),
				"OPENAI_API_KEY": "api-key",
			},
			want: openAIRequestCredentials{
				token:    "api-key",
				authType: common.ProviderAuthTypeAPI,
			},
			wantCalls: []string{"OPENAI_API_KEY"},
		},
		{
			name:     "subscription does not fall back",
			authType: common.ProviderAuthTypeSubscription,
			secrets: map[string]string{
				"OPENAI_API_KEY": "api-key",
			},
			wantCalls:         []string{"OPENAI_OAUTH"},
			wantErrorContains: "requires OAuth credentials",
		},
		{
			name:     "retrieval error does not fall back",
			authType: common.ProviderAuthTypeAny,
			secrets: map[string]string{
				"OPENAI_API_KEY": "api-key",
			},
			errs: map[string]error{
				"OPENAI_OAUTH": errors.New("keyring unavailable"),
			},
			wantCalls:         []string{"OPENAI_OAUTH"},
			wantErrorContains: "keyring unavailable",
		},
		{
			name:     "malformed OAuth does not fall back",
			authType: common.ProviderAuthTypeAny,
			secrets: map[string]string{
				"OPENAI_OAUTH":   "not-json",
				"OPENAI_API_KEY": "api-key",
			},
			wantCalls:         []string{"OPENAI_OAUTH"},
			wantErrorContains: "failed to parse OpenAI OAuth credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &openAIAuthTestSecretManager{
				secrets: tt.secrets,
				errs:    tt.errs,
			}

			got, err := openAICredentialsForRequest(manager, "OPENAI", tt.authType)

			if tt.wantErrorContains != "" {
				require.ErrorContains(t, err, tt.wantErrorContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			assert.Equal(t, tt.wantCalls, manager.calls)
		})
	}
}

func TestOpenAICredentialsForRequest_UsesLiteralOAuthSecretName(t *testing.T) {
	oauthJSON, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "oauth-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    9999999999,
		AccountID:    "account-id",
	})
	require.NoError(t, err)

	manager := &openAIAuthTestSecretManager{
		secrets: map[string]string{
			openai_oauth.SecretName: string(oauthJSON),
		},
	}

	credentials, err := openAICredentialsForRequest(
		manager,
		"OPENAI_ALIAS",
		common.ProviderAuthTypeSubscription,
	)

	require.NoError(t, err)
	assert.Equal(t, openAIRequestCredentials{
		token:     "oauth-token",
		accountID: "account-id",
		useOAuth:  true,
		authType:  common.ProviderAuthTypeSubscription,
	}, credentials)
	assert.Equal(t, []string{openai_oauth.SecretName}, manager.calls)
}
