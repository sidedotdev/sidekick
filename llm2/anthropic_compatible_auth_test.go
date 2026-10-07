package llm2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anthropicCompatibleTestRequest(t *testing.T) StreamRequest {
	t.Helper()

	oauthJSON, err := json.Marshal(llm.OAuthCredentials{
		AccessToken:  "oauth-access-token",
		RefreshToken: "oauth-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	return StreamRequest{
		Messages: []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hello"}},
		}},
		Options: Options{ModelConfig: common.ModelConfig{
			Provider: "vendor-anthropic",
			Model:    "vendor-model",
		}},
		SecretManager: anthropicAuthTestSecretManager{
			secrets: map[string]string{
				llm.AnthropicOAuthSecretName: string(oauthJSON),
				"VENDOR_ANTHROPIC_API_KEY":   "sk-vendor-key",
			},
		},
	}
}

func TestAnthropicProvider_CompatibleUsesProviderAPIKey(t *testing.T) {
	t.Parallel()

	for _, authType := range []common.ProviderAuthType{common.ProviderAuthTypeAny, common.ProviderAuthTypeAPI, ""} {
		t.Run(string(authType), func(t *testing.T) {
			t.Parallel()

			var apiKey, authorization, anthropicBeta string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiKey = r.Header.Get("X-Api-Key")
				authorization = r.Header.Get("Authorization")
				anthropicBeta = r.Header.Get("anthropic-beta")
				http.Error(w, "stop after request inspection", http.StatusUnauthorized)
			}))
			defer server.Close()

			provider := AnthropicProvider{
				BaseURL:             server.URL,
				AnthropicCompatible: true,
				AuthType:            authType,
			}

			_, err := provider.Stream(context.Background(), anthropicCompatibleTestRequest(t), make(chan Event, 1))

			assert.Error(t, err)
			assert.Equal(t, "sk-vendor-key", apiKey)
			assert.Empty(t, authorization)
			assert.NotContains(t, anthropicBeta, anthropicOAuthBetaHeader)
		})
	}
}

func TestAnthropicProvider_CompatibleSubscriptionRejectedBeforeRequest(t *testing.T) {
	t.Parallel()

	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := AnthropicProvider{
		BaseURL:             server.URL,
		AnthropicCompatible: true,
		AuthType:            common.ProviderAuthTypeSubscription,
	}

	_, err := provider.Stream(context.Background(), anthropicCompatibleTestRequest(t), make(chan Event, 1))

	assert.ErrorContains(t, err, "Anthropic subscription auth is only supported by anthropic provider types")
	assert.False(t, requested)
}
