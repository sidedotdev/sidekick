package llm2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/require"
)

// Native providers are exercised with an empty BaseURL, routed to the test
// server via the SDK's OPENAI_BASE_URL environment variable, so the subtests
// cannot run in parallel.
func TestOpenAIProviders_PromptCacheKey(t *testing.T) {
	const flowId = "flow-123"

	providers := []struct {
		name   string
		path   string
		stream func(baseURL string, request StreamRequest) error
	}{
		{
			name: "chat_completions",
			path: "/chat/completions",
			stream: func(baseURL string, request StreamRequest) error {
				provider := OpenAIProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAny}
				_, err := provider.Stream(context.Background(), request, make(chan Event, 1))
				return err
			},
		},
		{
			name: "responses",
			path: "/responses",
			stream: func(baseURL string, request StreamRequest) error {
				provider := OpenAIResponsesProvider{BaseURL: baseURL, AuthType: common.ProviderAuthTypeAny}
				_, err := provider.Stream(context.Background(), request, make(chan Event, 1))
				return err
			},
		},
	}

	cases := []struct {
		name        string
		native      bool
		key         string
		wantPresent bool
	}{
		{name: "native with key", native: true, key: flowId, wantPresent: true},
		{name: "native without key", native: true, key: "", wantPresent: false},
		{name: "compatible with key", native: false, key: flowId, wantPresent: false},
	}

	for _, p := range providers {
		for _, tc := range cases {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				var requestPath string
				var body map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestPath = r.URL.Path
					raw, err := io.ReadAll(r.Body)
					if err == nil {
						_ = json.Unmarshal(raw, &body)
					}
					http.Error(w, "stop after request inspection", http.StatusUnauthorized)
				}))
				defer server.Close()

				baseURL := server.URL
				if tc.native {
					t.Setenv("OPENAI_BASE_URL", server.URL)
					baseURL = ""
				}

				request := StreamRequest{
					Messages: []Message{{
						Role:    RoleUser,
						Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hello"}},
					}},
					Options: Options{
						PromptCacheKey: tc.key,
						ModelConfig: common.ModelConfig{
							Provider: "openai",
							Model:    "gpt-5-codex",
						},
					},
					SecretManager: &openAIAuthTestSecretManager{
						secrets: map[string]string{"OPENAI_API_KEY": "api-key"},
					},
				}

				err := p.stream(baseURL, request)

				require.Error(t, err)
				require.Equal(t, p.path, requestPath, "request must reach the test server")
				require.NotNil(t, body, "request body must be JSON")
				value, present := body["prompt_cache_key"]
				require.Equal(t, tc.wantPresent, present, "prompt_cache_key presence")
				if tc.wantPresent {
					require.Equal(t, tc.key, value)
				}
			})
		}
	}
}
