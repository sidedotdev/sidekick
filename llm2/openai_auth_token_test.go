package llm2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

// A provider configured with an auth token authenticates with it directly:
// its holder owns the provider credentials, so no secret is read and the
// configured auth type alone decides how the request is shaped.
func TestOpenAIResponsesProviderAuthToken(t *testing.T) {
	for _, tc := range []struct {
		authType      common.ProviderAuthType
		wantMaxTokens bool
	}{
		{common.ProviderAuthTypeAPI, true},
		{common.ProviderAuthTypeSubscription, false},
	} {
		t.Run(string(tc.authType), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer gateway-token", r.Header.Get("Authorization"))
				_, hasAccount := r.Header["Chatgpt-Account-Id"]
				require.False(t, hasAccount, "no account is known for a caller-supplied token")
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				_, hasMaxTokens := body["max_output_tokens"]
				require.Equal(t, tc.wantMaxTokens, hasMaxTokens)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n")
				w.(http.Flusher).Flush()
			}))
			defer upstream.Close()

			manager := &openAIAuthTestSecretManager{}
			provider := OpenAIResponsesProvider{
				BaseURL:   upstream.URL,
				AuthType:  tc.authType,
				AuthToken: "gateway-token",
			}
			response, err := provider.Stream(context.Background(), StreamRequest{
				Messages: []Message{{Role: RoleUser, Content: TextContentBlocks("Hello")}},
				Options: Options{
					ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"},
					MaxTokens:   32,
				},
				SecretManager: manager,
			}, make(chan Event, 16))
			require.NoError(t, err)
			require.Empty(t, manager.calls, "a configured auth token replaces secret lookup")
			require.Equal(t, tc.authType, response.AuthType)
			require.Equal(t, 7, response.Usage.InputTokens)
		})
	}
}

func TestOpenAIProviderAuthToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer gateway-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	manager := &openAIAuthTestSecretManager{}
	provider := OpenAIProvider{BaseURL: upstream.URL, AuthToken: "gateway-token"}
	response, err := provider.Stream(context.Background(), StreamRequest{
		Messages:      []Message{{Role: RoleUser, Content: TextContentBlocks("Hello")}},
		Options:       Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5.2"}},
		SecretManager: manager,
	}, make(chan Event, 16))
	require.NoError(t, err)
	require.Empty(t, manager.calls, "a configured auth token replaces secret lookup")
	require.Equal(t, common.ProviderAuthTypeAPI, response.AuthType)
}

// "any" resolves by reading OAuth credentials, which a caller-supplied token
// cannot stand in for, so the mode must be named instead of guessed.
func TestOpenAIResponsesProviderAuthTokenRequiresResolvedAuthType(t *testing.T) {
	manager := &openAIAuthTestSecretManager{}
	provider := OpenAIResponsesProvider{
		BaseURL:   "http://127.0.0.1:1",
		AuthType:  common.ProviderAuthTypeAny,
		AuthToken: "gateway-token",
	}
	_, err := provider.Stream(context.Background(), StreamRequest{
		Messages:      []Message{{Role: RoleUser, Content: TextContentBlocks("Hello")}},
		Options:       Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5-codex"}},
		SecretManager: manager,
	}, make(chan Event, 16))
	require.ErrorContains(t, err, "requires auth type")
	require.Empty(t, manager.calls)
}
