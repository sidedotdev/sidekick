package llm2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/openai_oauth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func alphaSearchTestSecrets(t *testing.T) *openAIAuthTestSecretManager {
	t.Helper()
	oauthJSON, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "oauth-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    9999999999,
		AccountID:    "account-id",
	})
	require.NoError(t, err)
	return &openAIAuthTestSecretManager{secrets: map[string]string{openai_oauth.SecretName: string(oauthJSON)}}
}

func TestOpenAIResponsesProvider_AlphaSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		queries    []string
		status     int
		body       string
		wantErr    string
		wantCalled bool
		want       *OpenAIAlphaSearchResponse
	}{
		{
			name:       "returns parsed results",
			queries:    []string{"current Go release"},
			status:     http.StatusOK,
			body:       `{"output":"Go 1.27","encrypted_output":"x","results":[{"type":"text_result","domain":"go.dev","ref_id":"turn0search0","title":"Go 1.27 Release Notes","url":"https://go.dev/doc/go1.27","snippet":"notes"}]}`,
			wantCalled: true,
			want: &OpenAIAlphaSearchResponse{
				Output: "Go 1.27",
				Results: []OpenAIAlphaSearchResult{{
					Type: "text_result", Domain: "go.dev", RefId: "turn0search0",
					Title: "Go 1.27 Release Notes", URL: "https://go.dev/doc/go1.27", Snippet: "notes",
				}},
			},
		},
		{
			name:       "non-200 is an error",
			queries:    []string{"q"},
			status:     http.StatusInternalServerError,
			body:       `{"error":"boom"}`,
			wantCalled: true,
			wantErr:    "alpha search returned HTTP 500",
		},
		{
			name:       "empty response is an error",
			queries:    []string{"q"},
			status:     http.StatusOK,
			body:       `{"results":[]}`,
			wantCalled: true,
			wantErr:    "neither output nor results",
		},
		{
			name:    "requires a query",
			wantErr: "at least one query",
		},
		{
			name:    "rejects blank queries",
			queries: []string{" "},
			wantErr: "must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/alpha/search", r.URL.Path)
				assert.Equal(t, "Bearer oauth-token", r.Header.Get("Authorization"))
				assert.Equal(t, "account-id", r.Header.Get("ChatGPT-Account-Id"))

				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var body struct {
					Id       string `json:"id"`
					Model    string `json:"model"`
					Commands struct {
						SearchQuery []map[string]string `json:"search_query"`
					} `json:"commands"`
					Settings        map[string]any `json:"settings"`
					MaxOutputTokens int            `json:"max_output_tokens"`
				}
				require.NoError(t, json.Unmarshal(raw, &body))
				assert.NotEmpty(t, body.Id)
				assert.Equal(t, "gpt-6-astra", body.Model)
				assert.Equal(t, []map[string]string{{"q": tc.queries[0]}}, body.Commands.SearchQuery)
				assert.Equal(t, true, body.Settings["external_web_access"])
				assert.Equal(t, defaultOpenAIAlphaSearchMaxOutputTokens, body.MaxOutputTokens)

				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			provider := OpenAIResponsesProvider{BaseURL: server.URL}
			response, err := provider.AlphaSearch(context.Background(), alphaSearchTestSecrets(t), OpenAIAlphaSearchRequest{
				Provider: "openai",
				Model:    "gpt-6-astra",
				Queries:  tc.queries,
			})
			assert.Equal(t, tc.wantCalled, called)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, response)
		})
	}
}
