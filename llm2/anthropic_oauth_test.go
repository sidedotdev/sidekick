package llm2

import (
	"encoding/json"
	"io"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// Not parallel: HTTP transport and keyring overrides are process-wide.
func TestGetAnthropicOAuthCredentials_RefreshPreservesProfile(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantErr  bool
		wantAuth bool
	}{
		{
			name:   "successful refresh",
			status: http.StatusOK,
			body:   `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}`,
		},
		{
			name:    "malformed response",
			status:  http.StatusOK,
			body:    `not-json`,
			wantErr: true,
		},
		{
			name:    "missing access token",
			status:  http.StatusOK,
			body:    `{"refresh_token":"new-refresh","expires_in":7200}`,
			wantErr: true,
		},
		{
			name:    "null response",
			status:  http.StatusOK,
			body:    `null`,
			wantErr: true,
		},
		{
			name:     "rejected refresh token",
			status:   http.StatusBadRequest,
			body:     `{"error":"invalid_grant"}`,
			wantErr:  true,
			wantAuth: true,
		},
		{
			name:    "service unavailable",
			status:  http.StatusServiceUnavailable,
			body:    `Service Unavailable`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			stale := `{"access_token":"old-access","refresh_token":"old-refresh","expires_at":1}`
			require.NoError(t, keyring.Set("sidekick", "WORK-"+llm.AnthropicOAuthSecretName, stale))
			require.NoError(t, keyring.Set("sidekick", llm.AnthropicOAuthSecretName, "default-profile"))

			requestCount := 0
			originalTransport := http.DefaultTransport
			http.DefaultTransport = anthropicRefreshTestTransport(func(req *http.Request) (*http.Response, error) {
				requestCount++
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
				var payload map[string]string
				require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
				assert.Equal(t, "refresh_token", payload["grant_type"])
				assert.Equal(t, "old-refresh", payload["refresh_token"])
				assert.NotEmpty(t, payload["client_id"])
				return &http.Response{
					StatusCode: tt.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    req,
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })

			before := time.Now().Unix()
			creds, found, err := GetAnthropicOAuthCredentials(secret_manager.KeyringSecretManager{ProfileId: "work"})
			assert.Equal(t, 1, requestCount)
			stored, storageErr := keyring.Get("sidekick", "WORK-"+llm.AnthropicOAuthSecretName)
			require.NoError(t, storageErr)
			defaultStored, storageErr := keyring.Get("sidekick", llm.AnthropicOAuthSecretName)
			require.NoError(t, storageErr)
			assert.Equal(t, "default-profile", defaultStored)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, creds)
				assert.False(t, found)
				assert.JSONEq(t, stale, stored)
				details, isAuth := common.AsProviderAuthError(err)
				require.Equal(t, tt.wantAuth, isAuth)
				if tt.wantAuth {
					assert.Equal(t, "work", details.ProfileId)
					assert.Equal(t, common.ProviderAuthErrorReasonInvalidOAuth, details.Reason)
				}
				return
			}

			require.NoError(t, err)
			require.True(t, found)
			require.NotNil(t, creds)
			assert.Equal(t, "new-access", creds.AccessToken)
			assert.Equal(t, "new-refresh", creds.RefreshToken)
			assert.GreaterOrEqual(t, creds.ExpiresAt, before+7200)
			assert.LessOrEqual(t, creds.ExpiresAt, time.Now().Unix()+7200)
			var persisted llm.OAuthCredentials
			require.NoError(t, json.Unmarshal([]byte(stored), &persisted))
			assert.Equal(t, *creds, persisted)
		})
	}
}
