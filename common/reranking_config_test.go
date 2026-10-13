package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalRerankingConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    string
		provider  string
		model     string
		useCase   string
		wantError string
	}{
		{
			name: "builtin without a provider declaration",
			config: `reranking:
  default:
    - provider: cohere
`,
			provider: "cohere",
		},
		{
			name: "inferred builtin provider name",
			config: `providers:
  - type: cohere
    key: secret
reranking:
  defaults:
    - provider: cohere
`,
			provider: "cohere",
		},
		{
			name: "named provider and model",
			config: `providers:
  - name: private-ranking
    type: cohere
    key: secret
reranking:
  default:
    - provider: private-ranking
      model: rerank-v3.5
  diff_summarize:
    - provider: private-ranking
      model: rerank-v4.0-fast
`,
			provider: "private-ranking",
			model:    "rerank-v3.5",
			useCase:  "rerank-v4.0-fast",
		},
		{
			name: "unknown provider",
			config: `llm:
  default:
    - provider: openai
reranking:
  default:
    - provider: nonexistent
`,
			wantError: "invalid provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.yml")
			require.NoError(t, os.WriteFile(path, []byte(tt.config), 0600))
			config, err := LoadSidekickConfig(path)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			public, err := localPublicConfigFrom(config)
			require.NoError(t, err)

			data, err := json.Marshal(public)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "secret")
			var decoded struct {
				Reranking EmbeddingConfig `json:"reranking"`
			}
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Len(t, decoded.Reranking.Defaults, 1)
			assert.Equal(t, tt.provider, decoded.Reranking.Defaults[0].Provider)
			assert.Equal(t, tt.model, decoded.Reranking.Defaults[0].Model)
			if tt.useCase != "" {
				require.Len(t, decoded.Reranking.UseCaseConfigs["diff_summarize"], 1)
				assert.Equal(t, tt.useCase, decoded.Reranking.UseCaseConfigs["diff_summarize"][0].Model)
			}
		})
	}
}
