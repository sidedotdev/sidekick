package provider_auth

import (
	"encoding/json"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/openai_oauth"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// stubKeyring replaces keyring access with an in-memory store keyed by the
// derived secret name.
func stubKeyring(t *testing.T, initial map[string]string) map[string]string {
	t.Helper()

	origSet, origGet, origDelete := KeyringSet, KeyringGet, KeyringDelete
	t.Cleanup(func() {
		KeyringSet, KeyringGet, KeyringDelete = origSet, origGet, origDelete
	})

	store := map[string]string{}
	for k, v := range initial {
		store[k] = v
	}
	KeyringSet = func(service, key, value string) error {
		store[key] = value
		return nil
	}
	KeyringGet = func(service, key string) (string, error) {
		if value, ok := store[key]; ok {
			return value, nil
		}
		return "", keyring.ErrNotFound
	}
	KeyringDelete = func(service, key string) error {
		if _, ok := store[key]; !ok {
			return keyring.ErrNotFound
		}
		delete(store, key)
		return nil
	}
	return store
}

func TestSaveOpenAIOAuthCredentialsPerProfile(t *testing.T) {
	store := stubKeyring(t, nil)

	creds := &openai_oauth.Credentials{
		AccessToken:  "access",
		RefreshToken: "refresh",
		AccountID:    "account",
	}
	require.NoError(t, SaveOpenAIOAuthCredentials(creds, []string{common.DefaultProfileId, "work"}))

	credsJSON, err := json.Marshal(creds)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		openai_oauth.SecretName:           string(credsJSON),
		"WORK-" + openai_oauth.SecretName: string(credsJSON),
	}, store)
}

func TestSaveAnthropicOAuthCredentialsPerProfile(t *testing.T) {
	store := stubKeyring(t, nil)

	creds := AnthropicOAuthCredentials{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    123,
	}
	require.NoError(t, SaveAnthropicOAuthCredentials(creds, []string{"work"}))

	assert.Equal(t, map[string]string{
		"WORK-" + llm.AnthropicOAuthSecretName: `{"access_token":"access","refresh_token":"refresh","expires_at":123}`,
	}, store)
}

func TestSaveAPIKey(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		profileId string
		apiKey    string
		want      map[string]string
		wantErr   error
	}{
		{
			name:     "anthropic default profile",
			provider: "anthropic",
			apiKey:   " sk-ant \n",
			want:     map[string]string{llm.AnthropicApiKeySecretName: "sk-ant"},
		},
		{
			name:      "openai named profile",
			provider:  "OpenAI",
			profileId: "work",
			apiKey:    "sk-openai",
			want:      map[string]string{"WORK-" + llm.OpenaiApiKeySecretName: "sk-openai"},
		},
		{
			name:     "google",
			provider: "google",
			apiKey:   "g-key",
			want:     map[string]string{llm.GoogleApiKeySecretName: "g-key"},
		},
		{
			name:     "unknown provider",
			provider: "acme",
			apiKey:   "key",
			want:     map[string]string{},
			wantErr:  ErrUnsupportedProvider,
		},
		{
			name:     "blank key",
			provider: "anthropic",
			apiKey:   "   ",
			want:     map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := stubKeyring(t, nil)

			err := SaveAPIKey(tc.provider, tc.profileId, tc.apiKey)
			switch {
			case tc.wantErr != nil:
				assert.ErrorIs(t, err, tc.wantErr)
			case len(tc.want) == 0:
				assert.Error(t, err)
			default:
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.want, store)
		})
	}
}

func TestDeleteOAuthCredential(t *testing.T) {
	store := stubKeyring(t, map[string]string{
		llm.AnthropicOAuthSecretName:           "default-creds",
		"WORK-" + llm.AnthropicOAuthSecretName: "work-creds",
		"WORK-" + openai_oauth.SecretName:      "openai-work-creds",
	})

	require.NoError(t, DeleteOAuthCredential("anthropic", "work"))
	require.NoError(t, DeleteOAuthCredential("openai", common.DefaultProfileId), "a credential absent from the keyring is not an error")
	assert.ErrorIs(t, DeleteOAuthCredential("google", "work"), ErrUnsupportedProvider)

	assert.Equal(t, map[string]string{
		llm.AnthropicOAuthSecretName:      "default-creds",
		"WORK-" + openai_oauth.SecretName: "openai-work-creds",
	}, store)
}
