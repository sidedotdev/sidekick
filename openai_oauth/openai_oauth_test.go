package openai_oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + payloadEnc + ".fakesig"
}

func validJWTClaims(accountID string) map[string]any {
	return map[string]any{
		"sub": "user-123",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
}

func TestParseCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "valid camelCase credentials",
			input: `{"accessToken":"at","refreshToken":"rt","expiresAt":9999999999,"accountId":"acct-1"}`,
		},
		{
			name:  "valid legacy snake_case credentials",
			input: `{"access_token":"at","refresh_token":"rt","expires_at":9999999999,"account_id":"acct-1"}`,
		},
		{
			name:    "invalid JSON",
			input:   `{bad`,
			wantErr: "failed to parse OpenAI OAuth credentials",
		},
		{
			name:    "missing accessToken",
			input:   `{"refreshToken":"rt","expiresAt":123,"accountId":"a"}`,
			wantErr: "missing accessToken",
		},
		{
			name:    "missing refreshToken",
			input:   `{"accessToken":"at","expiresAt":123,"accountId":"a"}`,
			wantErr: "missing refreshToken",
		},
		{
			name:    "missing expiresAt",
			input:   `{"accessToken":"at","refreshToken":"rt","accountId":"a"}`,
			wantErr: "missing expiresAt",
		},
		{
			name:    "missing accountId",
			input:   `{"accessToken":"at","refreshToken":"rt","expiresAt":123}`,
			wantErr: "missing accountId",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			creds, err := ParseCredentials(tc.input)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, creds)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "at", creds.AccessToken)
				assert.Equal(t, "rt", creds.RefreshToken)
				assert.Equal(t, int64(9999999999), creds.ExpiresAt)
				assert.Equal(t, "acct-1", creds.AccountID)
			}
		})
	}
}

func TestExtractAccountIDFromAccessToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   string
		wantID  string
		wantErr string
	}{
		{
			name:   "valid JWT with account_id",
			token:  makeJWT(validJWTClaims("acct-abc")),
			wantID: "acct-abc",
		},
		{
			name:    "not a JWT",
			token:   "not-a-jwt",
			wantErr: "expected at least 2 parts",
		},
		{
			name:    "bad base64 payload",
			token:   "header.!!!invalid!!!.sig",
			wantErr: "failed to decode JWT payload",
		},
		{
			name: "no recognized claims at all",
			token: makeJWT(map[string]any{
				"sub": "user",
			}),
			wantErr: "no recognized claim shape found",
		},
		{
			name: "auth claim not an object falls through",
			token: makeJWT(map[string]any{
				"https://api.openai.com/auth": "not-an-object",
			}),
			wantErr: "no recognized claim shape found",
		},
		{
			name: "auth claim with empty chatgpt_account_id falls through",
			token: makeJWT(map[string]any{
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "",
				},
			}),
			wantErr: "no recognized claim shape found",
		},
		{
			name: "alternate: top-level chatgpt_account_id",
			token: makeJWT(map[string]any{
				"chatgpt_account_id": "acct-toplevel",
			}),
			wantID: "acct-toplevel",
		},
		{
			name: "alternate: organizations array under auth claim",
			token: makeJWT(map[string]any{
				"https://api.openai.com/auth": map[string]any{
					"organizations": []any{
						map[string]any{"id": "org-from-array"},
					},
				},
			}),
			wantID: "org-from-array",
		},
		{
			name: "primary path preferred over top-level",
			token: makeJWT(map[string]any{
				"chatgpt_account_id": "acct-toplevel",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acct-primary",
				},
			}),
			wantID: "acct-primary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := ExtractAccountIDFromAccessToken(tc.token)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantID, id)
			}
		})
	}
}

func TestExchangeRefreshToken(t *testing.T) {
	validAccessToken := makeJWT(validJWTClaims("acct-refreshed"))

	tests := []struct {
		name       string
		statusCode int
		response   any
		wantErr    string
	}{
		{
			name:       "successful refresh",
			statusCode: http.StatusOK,
			response: map[string]any{
				"access_token":  validAccessToken,
				"refresh_token": "new-rt",
				"expires_in":    3600,
			},
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			response:   map[string]any{"error": "server_error"},
			wantErr:    "token refresh failed with status 500",
		},
		{
			name:       "missing access_token in response",
			statusCode: http.StatusOK,
			response: map[string]any{
				"refresh_token": "rt",
				"expires_in":    3600,
			},
			wantErr: "missing access_token",
		},
		{
			name:       "missing refresh_token in response",
			statusCode: http.StatusOK,
			response: map[string]any{
				"access_token": validAccessToken,
				"expires_in":   3600,
			},
			wantErr: "missing refresh_token",
		},
		{
			name:       "missing expires_in in response",
			statusCode: http.StatusOK,
			response: map[string]any{
				"access_token":  validAccessToken,
				"refresh_token": "rt",
			},
			wantErr: "missing expires_in",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

				err := r.ParseForm()
				require.NoError(t, err)
				assert.Equal(t, "refresh_token", r.FormValue("grant_type"))
				assert.Equal(t, ClientID, r.FormValue("client_id"))
				assert.Equal(t, "test-refresh-token", r.FormValue("refresh_token"))

				w.WriteHeader(tc.statusCode)
				json.NewEncoder(w).Encode(tc.response)
			}))
			defer server.Close()

			origEndpoint := TokenEndpoint
			origClient := HTTPClient
			TokenEndpoint = server.URL
			HTTPClient = server.Client()
			defer func() {
				TokenEndpoint = origEndpoint
				HTTPClient = origClient
			}()

			creds, err := ExchangeRefreshToken(context.Background(), "test-refresh-token")
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, creds)
			} else {
				require.NoError(t, err)
				assert.Equal(t, validAccessToken, creds.AccessToken)
				assert.Equal(t, "new-rt", creds.RefreshToken)
				assert.Equal(t, "acct-refreshed", creds.AccountID)
				assert.InDelta(t, time.Now().Unix()+3600, creds.ExpiresAt, 5)
			}
		})
	}
}

type mockSecretManager struct {
	secrets  map[string]string
	forceErr error
}

func (m *mockSecretManager) GetSecret(name string) (string, error) {
	if m.forceErr != nil {
		return "", m.forceErr
	}
	v, ok := m.secrets[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, name)
	}
	return v, nil
}

func TestGetAndMaybeRefresh(t *testing.T) {
	ctx := context.Background()
	validAccessToken := makeJWT(validJWTClaims("acct-test"))

	t.Run("no credentials configured (not found error)", func(t *testing.T) {
		sm := &mockSecretManager{secrets: map[string]string{}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, creds)
	})

	t.Run("non-not-found GetSecret error propagated", func(t *testing.T) {
		sm := &mockSecretManager{secrets: map[string]string{}, forceErr: fmt.Errorf("keyring locked")}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keyring locked")
		assert.False(t, ok)
		assert.Nil(t, creds)
	})

	t.Run("valid non-expired credentials returned as-is", func(t *testing.T) {
		raw, _ := json.Marshal(Credentials{
			AccessToken:  "at-valid",
			RefreshToken: "rt-valid",
			ExpiresAt:    time.Now().Unix() + 3600,
			AccountID:    "acct-ok",
		})
		sm := &mockSecretManager{secrets: map[string]string{SecretName: string(raw)}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "at-valid", creds.AccessToken)
		assert.Equal(t, "acct-ok", creds.AccountID)
	})

	t.Run("expired credentials trigger refresh", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  validAccessToken,
				"refresh_token": "new-rt",
				"expires_in":    7200,
			})
		}))
		defer server.Close()

		origEndpoint := TokenEndpoint
		origClient := HTTPClient
		origStore := StoreFn
		TokenEndpoint = server.URL
		HTTPClient = server.Client()

		var stored *Credentials
		StoreFn = func(c *Credentials) error {
			stored = c
			return nil
		}
		defer func() {
			TokenEndpoint = origEndpoint
			HTTPClient = origClient
			StoreFn = origStore
		}()

		raw, _ := json.Marshal(Credentials{
			AccessToken:  "at-expired",
			RefreshToken: "rt-old",
			ExpiresAt:    time.Now().Unix() - 60,
			AccountID:    "acct-old",
		})
		sm := &mockSecretManager{secrets: map[string]string{SecretName: string(raw)}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, validAccessToken, creds.AccessToken)
		assert.Equal(t, "new-rt", creds.RefreshToken)
		assert.Equal(t, "acct-test", creds.AccountID)
		require.NotNil(t, stored)
		assert.Equal(t, creds.AccessToken, stored.AccessToken)
	})

	t.Run("near-expiry credentials trigger refresh", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  validAccessToken,
				"refresh_token": "new-rt-2",
				"expires_in":    7200,
			})
		}))
		defer server.Close()

		origEndpoint := TokenEndpoint
		origClient := HTTPClient
		origStore := StoreFn
		TokenEndpoint = server.URL
		HTTPClient = server.Client()
		StoreFn = func(c *Credentials) error { return nil }
		defer func() {
			TokenEndpoint = origEndpoint
			HTTPClient = origClient
			StoreFn = origStore
		}()

		raw, _ := json.Marshal(Credentials{
			AccessToken:  "at-soon",
			RefreshToken: "rt-soon",
			ExpiresAt:    time.Now().Unix() + 100, // within 5 min window
			AccountID:    "acct-soon",
		})
		sm := &mockSecretManager{secrets: map[string]string{SecretName: string(raw)}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, validAccessToken, creds.AccessToken)
	})

	t.Run("store failure after refresh propagates error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  validAccessToken,
				"refresh_token": "new-rt-3",
				"expires_in":    7200,
			})
		}))
		defer server.Close()

		origEndpoint := TokenEndpoint
		origClient := HTTPClient
		origStore := StoreFn
		TokenEndpoint = server.URL
		HTTPClient = server.Client()
		StoreFn = func(c *Credentials) error {
			return fmt.Errorf("disk full")
		}
		defer func() {
			TokenEndpoint = origEndpoint
			HTTPClient = origClient
			StoreFn = origStore
		}()

		raw, _ := json.Marshal(Credentials{
			AccessToken:  "at-expired",
			RefreshToken: "rt-old",
			ExpiresAt:    time.Now().Unix() - 60,
			AccountID:    "acct-old",
		})
		sm := &mockSecretManager{secrets: map[string]string{SecretName: string(raw)}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disk full")
		assert.False(t, ok)
		assert.Nil(t, creds)
	})

	t.Run("malformed credentials return error", func(t *testing.T) {
		sm := &mockSecretManager{secrets: map[string]string{SecretName: "{bad json"}}
		creds, ok, err := GetAndMaybeRefresh(ctx, sm)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Nil(t, creds)
	})
}
func TestCredentialsMarshalCamelCase(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Credentials{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    123,
		AccountID:    "acct",
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"accessToken":"at","refreshToken":"rt","expiresAt":123,"accountId":"acct"}`, string(raw))
}
