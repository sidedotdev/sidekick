package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRerankSecrets struct {
	names []string
	key   string
}

func (s *recordingRerankSecrets) GetSecret(name string) (string, error) {
	s.names = append(s.names, name)
	return s.key, nil
}

func (*recordingRerankSecrets) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

type rerankRoundTripper func(*http.Request) (*http.Response, error)

func (f rerankRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestConfiguredRerankerRequests(t *testing.T) {
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	for _, model := range []string{"", "rerank-v3.5"} {
		t.Run("model="+model, func(t *testing.T) {
			wantModel := model
			if wantModel == "" {
				wantModel = common.DefaultCohereRerankingModel
			}
			var tokenizeCalls, rerankCalls int
			http.DefaultTransport = rerankRoundTripper(func(request *http.Request) (*http.Response, error) {
				assert.Equal(t, "ranking.invalid", request.URL.Host)
				assert.Equal(t, "Bearer selected-key", request.Header.Get("Authorization"))
				var body struct {
					Model string `json:"model"`
				}
				require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
				assert.Equal(t, wantModel, body.Model)

				var response string
				switch {
				case strings.HasSuffix(request.URL.Path, "/tokenize"):
					tokenizeCalls++
					response = `{"tokens":[1]}`
				case strings.HasSuffix(request.URL.Path, "/rerank"):
					rerankCalls++
					response = `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]}`
				default:
					return nil, fmt.Errorf("unexpected Cohere request: %s", request.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(response)),
				}, nil
			})

			secrets := &recordingRerankSecrets{key: "  selected-key \n"}
			reranker, err := GetReranker(
				common.ModelConfig{Provider: "company-ranking", Model: model},
				[]common.ModelProviderPublicConfig{{Name: "company-ranking", Type: "cohere", BaseURL: "https://ranking.invalid"}},
				secrets,
			)
			require.NoError(t, err)
			require.NotNil(t, reranker)
			assert.Equal(t, []string{"COMPANY_RANKING_API_KEY"}, secrets.names)

			result, err := reranker.Rerank(context.Background(), "query", []string{"first", "second"})
			require.NoError(t, err)
			assert.Equal(t, []string{"second", "first"}, result)
			assert.Positive(t, tokenizeCalls)
			assert.Equal(t, 1, rerankCalls)
		})
	}
}

func TestRerankerUnavailableSelection(t *testing.T) {
	t.Parallel()
	secrets := &recordingRerankSecrets{key: "must-not-be-used"}
	reranker, err := GetReranker(common.ModelConfig{}, nil, secrets)
	require.NoError(t, err)
	assert.Nil(t, reranker)
	assert.Empty(t, secrets.names)
}

func TestRerankerRejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()
	reranker, err := GetReranker(
		common.ModelConfig{Provider: "ranking"},
		[]common.ModelProviderPublicConfig{{Name: "ranking", Type: "openai"}},
		&recordingRerankSecrets{key: "key"},
	)
	require.ErrorContains(t, err, "unsupported reranking provider")
	assert.Nil(t, reranker)
}
