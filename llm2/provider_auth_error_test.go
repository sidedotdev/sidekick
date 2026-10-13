package llm2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/openai_oauth"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func providerAuthTestRequest(provider, model string, secrets map[string]string) StreamRequest {
	return StreamRequest{
		Messages: []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hello"}},
		}},
		Options:       Options{ModelConfig: common.ModelConfig{Provider: provider, Model: model}},
		SecretManager: anthropicAuthTestSecretManager{secrets: secrets},
	}
}

func newProviderAuthStatusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func assertProviderAuthError(t *testing.T, err error, want *common.ProviderAuthErrorDetails) {
	t.Helper()
	require.Error(t, err)
	details, ok := common.AsProviderAuthError(err)
	if want == nil {
		assert.False(t, ok, "unexpected provider auth error: %v", err)
		return
	}
	require.True(t, ok, "expected provider auth error, got: %v", err)
	assert.Equal(t, *want, *details)
}

func TestProviderStream_AuthRejections(t *testing.T) {
	t.Parallel()

	anthropicOAuth, err := json.Marshal(llm.OAuthCredentials{
		AccessToken:  "oauth-access-token",
		RefreshToken: "oauth-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	openAIOAuth, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "oauth-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    9999999999,
		AccountID:    "account-id",
	})
	require.NoError(t, err)

	const anthropicUnauthorized = `{"type":"error","error":{"type":"authentication_error","message":"invalid credentials"}}`
	const openAIUnauthorized = `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`
	const serverError = `{"error":{"message":"internal error","type":"server_error"}}`

	tests := []struct {
		name        string
		newProvider func(baseURL string) Provider
		provider    string
		model       string
		secrets     map[string]string
		status      int
		body        string
		want        *common.ProviderAuthErrorDetails
	}{
		{
			name: "anthropic API key rejected",
			newProvider: func(baseURL string) Provider {
				return AnthropicProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAPI}
			},
			provider: "claude",
			model:    "claude-opus-4-5",
			secrets:  map[string]string{"CLAUDE_API_KEY": "sk-bad"},
			status:   http.StatusUnauthorized,
			body:     anthropicUnauthorized,
			want: &common.ProviderAuthErrorDetails{
				Provider: "claude", ProfileId: common.DefaultProfileId,
				AuthType: common.ProviderAuthTypeAPI, Reason: common.ProviderAuthErrorReasonInvalidAPIKey,
			},
		},
		{
			name: "anthropic OAuth rejected",
			newProvider: func(baseURL string) Provider {
				return AnthropicProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeSubscription}
			},
			provider: "anthropic",
			model:    "claude-opus-4-5",
			secrets:  map[string]string{llm.AnthropicOAuthSecretName: string(anthropicOAuth)},
			status:   http.StatusUnauthorized,
			body:     anthropicUnauthorized,
			want: &common.ProviderAuthErrorDetails{
				Provider: "anthropic", ProfileId: common.DefaultProfileId,
				AuthType: common.ProviderAuthTypeSubscription, Reason: common.ProviderAuthErrorReasonInvalidOAuth,
			},
		},
		{
			name: "anthropic server error is not an auth error",
			newProvider: func(baseURL string) Provider {
				return AnthropicProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAPI}
			},
			provider: "anthropic",
			model:    "claude-opus-4-5",
			secrets:  map[string]string{"ANTHROPIC_API_KEY": "sk-ok"},
			status:   http.StatusInternalServerError,
			body:     serverError,
		},
		{
			name: "openai chat API key rejected",
			newProvider: func(baseURL string) Provider {
				return OpenAIProvider{BaseURL: baseURL}
			},
			provider: "openai",
			model:    "gpt-5.2",
			secrets:  map[string]string{"OPENAI_API_KEY": "sk-bad"},
			status:   http.StatusUnauthorized,
			body:     openAIUnauthorized,
			want: &common.ProviderAuthErrorDetails{
				Provider: "openai", ProfileId: common.DefaultProfileId,
				AuthType: common.ProviderAuthTypeAPI, Reason: common.ProviderAuthErrorReasonInvalidAPIKey,
			},
		},
		{
			name: "openai chat server error is not an auth error",
			newProvider: func(baseURL string) Provider {
				return OpenAIProvider{BaseURL: baseURL}
			},
			provider: "openai",
			model:    "gpt-5.2",
			secrets:  map[string]string{"OPENAI_API_KEY": "sk-ok"},
			status:   http.StatusInternalServerError,
			body:     serverError,
		},
		{
			name: "openai responses API key rejected",
			newProvider: func(baseURL string) Provider {
				return OpenAIResponsesProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAPI}
			},
			provider: "openai",
			model:    "gpt-5.3-codex",
			secrets:  map[string]string{"OPENAI_API_KEY": "sk-bad"},
			status:   http.StatusUnauthorized,
			body:     openAIUnauthorized,
			want: &common.ProviderAuthErrorDetails{
				Provider: "openai", ProfileId: common.DefaultProfileId,
				AuthType: common.ProviderAuthTypeAPI, Reason: common.ProviderAuthErrorReasonInvalidAPIKey,
			},
		},
		{
			name: "openai responses OAuth rejected",
			newProvider: func(baseURL string) Provider {
				return OpenAIResponsesProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeSubscription}
			},
			provider: "openai",
			model:    "gpt-5.3-codex",
			secrets:  map[string]string{openai_oauth.SecretName: string(openAIOAuth)},
			status:   http.StatusUnauthorized,
			body:     openAIUnauthorized,
			want: &common.ProviderAuthErrorDetails{
				Provider: "openai", ProfileId: common.DefaultProfileId,
				AuthType: common.ProviderAuthTypeSubscription, Reason: common.ProviderAuthErrorReasonInvalidOAuth,
			},
		},
		{
			name: "openai responses server error is not an auth error",
			newProvider: func(baseURL string) Provider {
				return OpenAIResponsesProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAPI}
			},
			provider: "openai",
			model:    "gpt-5.3-codex",
			secrets:  map[string]string{"OPENAI_API_KEY": "sk-ok"},
			status:   http.StatusInternalServerError,
			body:     serverError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newProviderAuthStatusServer(t, tt.status, tt.body)
			provider := tt.newProvider(server.URL)

			_, err := provider.Stream(context.Background(), providerAuthTestRequest(tt.provider, tt.model, tt.secrets), make(chan Event, 100))

			assertProviderAuthError(t, err, tt.want)
		})
	}
}

// Not parallel: the Gemini base URL can only be overridden via environment.
func TestGoogleProviderStream_AuthRejections(t *testing.T) {
	const apiKeyInvalidBody = `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`
	const unauthenticatedBody = `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`
	const badRequestBody = `{"error":{"code":400,"message":"Invalid model","status":"INVALID_ARGUMENT"}}`

	invalidKey := &common.ProviderAuthErrorDetails{
		Provider: "google", ProfileId: common.DefaultProfileId,
		AuthType: common.ProviderAuthTypeAPI, Reason: common.ProviderAuthErrorReasonInvalidAPIKey,
	}

	tests := []struct {
		name   string
		status int
		body   string
		want   *common.ProviderAuthErrorDetails
	}{
		{name: "invalid API key reason", status: http.StatusBadRequest, body: apiKeyInvalidBody, want: invalidKey},
		{name: "unauthenticated", status: http.StatusUnauthorized, body: unauthenticatedBody, want: invalidKey},
		{name: "other bad request is not an auth error", status: http.StatusBadRequest, body: badRequestBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newProviderAuthStatusServer(t, tt.status, tt.body)
			t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

			_, err := GoogleProvider{}.Stream(
				context.Background(),
				providerAuthTestRequest("google", "gemini-3.1-pro-preview", map[string]string{"GOOGLE_API_KEY": "bad-key"}),
				make(chan Event, 100),
			)

			assertProviderAuthError(t, err, tt.want)
		})
	}
}

func TestGoogleProviderStream_MissingAPIKey(t *testing.T) {
	t.Parallel()

	_, err := GoogleProvider{}.Stream(
		context.Background(),
		providerAuthTestRequest("google", "gemini-3.1-pro-preview", map[string]string{}),
		make(chan Event, 100),
	)

	assertProviderAuthError(t, err, &common.ProviderAuthErrorDetails{
		Provider: "google", ProfileId: common.DefaultProfileId,
		AuthType: common.ProviderAuthTypeAPI, Reason: common.ProviderAuthErrorReasonMissingAPIKey,
	})
}

func TestIsProviderAuthRejection_IgnoresUnrelatedErrors(t *testing.T) {
	t.Parallel()

	assert.False(t, isProviderAuthRejection(errors.New("401 Unauthorized")))
	assert.False(t, isProviderAuthRejection(fmt.Errorf("wrapped: %w", context.Canceled)))
	assert.False(t, isProviderAuthRejection(nil))
}
