package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/openai_oauth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIDeviceAuthorization(t *testing.T) {
	var userCodeRequest map[string]string
	var tokenRequest map[string]string
	pollCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&userCodeRequest))
			json.NewEncoder(w).Encode(map[string]any{
				"device_auth_id": "device-id",
				"user_code":      "ABCD-EFGH",
				"interval":       0,
			})
		case "/token":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&tokenRequest))
			pollCount++
			if pollCount == 1 {
				http.Error(w, `{"error":"deviceauth_authorization_pending"}`, http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "authorization-code",
				"code_verifier":      "code-verifier",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	originalUserCodeEndpoint := openAIDeviceUserCodeEndpoint
	originalTokenEndpoint := openAIDeviceTokenEndpoint
	originalHTTPClient := openai_oauth.HTTPClient
	openAIDeviceUserCodeEndpoint = server.URL + "/usercode"
	openAIDeviceTokenEndpoint = server.URL + "/token"
	openai_oauth.HTTPClient = server.Client()
	defer func() {
		openAIDeviceUserCodeEndpoint = originalUserCodeEndpoint
		openAIDeviceTokenEndpoint = originalTokenEndpoint
		openai_oauth.HTTPClient = originalHTTPClient
	}()

	device, err := startOpenAIDeviceAuth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "device-id", device.DeviceAuthID)
	assert.Equal(t, "ABCD-EFGH", device.UserCode)
	assert.Equal(t, openai_oauth.ClientID, userCodeRequest["client_id"])

	token, err := pollOpenAIDeviceAuth(context.Background(), device)
	require.NoError(t, err)
	assert.Equal(t, "authorization-code", token.AuthorizationCode)
	assert.Equal(t, "code-verifier", token.CodeVerifier)
	assert.Equal(t, "device-id", tokenRequest["device_auth_id"])
	assert.Equal(t, "ABCD-EFGH", tokenRequest["user_code"])
	assert.Equal(t, 2, pollCount)
}
func TestPollOpenAIDeviceAuthDoesNotRetryTerminalErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
	}{
		{
			name:       "forbidden",
			statusCode: http.StatusForbidden,
			response:   `{"error":"access_denied"}`,
		},
		{
			name:       "not found",
			statusCode: http.StatusNotFound,
			response:   `{"error":"expired_token"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pollCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pollCount++
				http.Error(w, tt.response, tt.statusCode)
			}))
			defer server.Close()

			originalTokenEndpoint := openAIDeviceTokenEndpoint
			originalHTTPClient := openai_oauth.HTTPClient
			openAIDeviceTokenEndpoint = server.URL
			openai_oauth.HTTPClient = server.Client()
			defer func() {
				openAIDeviceTokenEndpoint = originalTokenEndpoint
				openai_oauth.HTTPClient = originalHTTPClient
			}()

			_, err := pollOpenAIDeviceAuth(context.Background(), &openAIDeviceAuthInfo{
				DeviceAuthID: "device-id",
				UserCode:     "ABCD-EFGH",
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.response)
			assert.Equal(t, 1, pollCount)
		})
	}
}
