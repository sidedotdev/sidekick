package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyAnthropicOutputLimits(t *testing.T) {
	common.ClearModelsCache()
	t.Cleanup(common.ClearModelsCache)
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "models.dev.json"), []byte(`{
		"anthropic": {"models": {
			"known": {"limit": {"output": 64000}},
			"no-output": {"limit": {"context": 200000}}
		}}
	}`), 0644))

	for _, tt := range []struct {
		name  string
		model string
		limit int
		want  int
	}{
		{name: "metadata", model: "known", want: 64000},
		{name: "explicit", model: "known", limit: 30000, want: 30000},
		{name: "capped", model: "known", limit: 100000, want: 64000},
		{name: "unknown", model: "unknown"},
		{name: "missing output", model: "no-output"},
		{name: "nonpositive", model: "unknown", limit: -1},
		{name: "unknown explicit", model: "unknown", limit: 1234, want: 1234},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"endpoint rejection"}}`, http.StatusBadRequest)
			}))
			defer server.Close()
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)
			options := ToolChatOptions{
				Params: ToolChatParams{
					ModelConfig: common.ModelConfig{Provider: "anthropic", Model: tt.model, MaxTokens: tt.limit},
					Messages:    []ChatMessage{{Role: ChatMessageRoleUser, Content: "Hello"}},
				},
				Secrets: secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
			}
			_, err := (AnthropicToolChat{}).ChatStream(context.Background(), options, make(chan ChatMessageDelta, 1), make(chan ProgressInfo, 1))
			require.ErrorContains(t, err, "endpoint rejection")
			require.NotNil(t, body, "request must reach the endpoint")
			if tt.want > 0 {
				assert.Equal(t, float64(tt.want), body["max_tokens"])
			} else {
				assert.NotContains(t, body, "max_tokens")
			}
		})
	}
}
