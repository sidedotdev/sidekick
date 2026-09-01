package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestStoreAnthropicOAuthCredentialsPerProfile(t *testing.T) {
	tests := []struct {
		name              string
		profileId         string
		expectedSecretKey string
	}{
		{
			name:              "default profile stores unprefixed key",
			profileId:         "default",
			expectedSecretKey: AnthropicOAuthSecretName,
		},
		{
			name:              "empty profile falls back to default",
			profileId:         "",
			expectedSecretKey: AnthropicOAuthSecretName,
		},
		{
			name:              "non-default profile stores prefixed key",
			profileId:         "work",
			expectedSecretKey: "WORK-" + AnthropicOAuthSecretName,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keyring.MockInit()

			creds := &OAuthCredentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: 123}
			require.NoError(t, storeAnthropicOAuthCredentialsForProfile(tc.profileId, creds))

			stored, err := keyring.Get(keyringService, tc.expectedSecretKey)
			require.NoError(t, err)

			expected, err := json.Marshal(creds)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), stored)
		})
	}
}

func TestRefreshedAnthropicCredentialsStayWithinTheirProfile(t *testing.T) {
	keyring.MockInit()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access",
			"refresh_token": "refreshed-refresh",
			"expires_in":    7200,
		})
	}))
	defer server.Close()

	origEndpoint := anthropicTokenEndpoint
	anthropicTokenEndpoint = server.URL
	t.Cleanup(func() { anthropicTokenEndpoint = origEndpoint })

	expired, err := json.Marshal(OAuthCredentials{
		AccessToken:  "stale-access",
		RefreshToken: "stale-refresh",
		ExpiresAt:    time.Now().Unix() - 60,
	})
	require.NoError(t, err)
	require.NoError(t, keyring.Set(keyringService, "WORK-"+AnthropicOAuthSecretName, string(expired)))

	creds, useOAuth, err := GetAnthropicOAuthCredentials(secret_manager.KeyringSecretManager{ProfileId: "work"})
	require.NoError(t, err)
	require.True(t, useOAuth)
	assert.Equal(t, "refreshed-access", creds.AccessToken)

	storedForWork, err := keyring.Get(keyringService, "WORK-"+AnthropicOAuthSecretName)
	require.NoError(t, err)
	assert.Contains(t, storedForWork, "refreshed-refresh")

	_, err = keyring.Get(keyringService, AnthropicOAuthSecretName)
	assert.ErrorIs(t, err, keyring.ErrNotFound, "refreshing a work-profile token must not overwrite default-profile credentials")
}
