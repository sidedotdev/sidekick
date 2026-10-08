package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profileMapSecretManager struct {
	secrets   map[string]string
	profileId string
}

func (m profileMapSecretManager) GetSecret(secretName string) (string, error) {
	if secret, ok := m.secrets[secretName]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (m profileMapSecretManager) GetProfileId() string {
	return m.profileId
}

// Not parallel: overrides the package-level token endpoint.
func TestGetAnthropicOAuthCredentials_ProviderAuthErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "invalid_grant", "error_description": "Refresh token not found or invalid"}`))
	}))
	defer server.Close()
	origEndpoint := anthropicTokenEndpoint
	anthropicTokenEndpoint = server.URL
	t.Cleanup(func() { anthropicTokenEndpoint = origEndpoint })

	expired, err := json.Marshal(OAuthCredentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
	require.NoError(t, err)

	tests := []struct {
		name          string
		oauthJSON     string
		wantErrDetail string
	}{
		{name: "refresh rejected", oauthJSON: string(expired), wantErrDetail: "invalid_grant"},
		{name: "malformed credentials", oauthJSON: "not-json", wantErrDetail: "failed to parse OAuth credentials"},
		{name: "missing access token", oauthJSON: `{"refresh_token":"r"}`, wantErrDetail: "missing access token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, useOAuth, err := GetAnthropicOAuthCredentials(profileMapSecretManager{
				secrets:   map[string]string{AnthropicOAuthSecretName: tt.oauthJSON},
				profileId: "work",
			})

			require.Error(t, err)
			assert.False(t, useOAuth)
			assert.Contains(t, err.Error(), tt.wantErrDetail)
			details, ok := common.AsProviderAuthError(err)
			require.True(t, ok)
			assert.Equal(t, common.ProviderAuthErrorDetails{
				Provider:  "anthropic",
				ProfileId: "work",
				AuthType:  common.ProviderAuthTypeSubscription,
				Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
			}, *details)
		})
	}
}

func TestGetAnthropicOAuthCredentials_MissingSecretIsNotAnError(t *testing.T) {
	t.Parallel()

	creds, useOAuth, err := GetAnthropicOAuthCredentials(profileMapSecretManager{secrets: map[string]string{}})

	require.NoError(t, err)
	assert.False(t, useOAuth)
	assert.Nil(t, creds)
}
