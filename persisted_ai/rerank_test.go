package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sidekick/common"
	"sidekick/secret_manager"
)

type rerankClientCall struct {
	query     string
	documents []string
}

type stubCohereRerankClient struct {
	calls         []rerankClientCall
	results       [][]int
	err           error
	tokenCount    func(string) int
	tokenizeErr   error
	tokenizeCalls []string
}

func (c *stubCohereRerankClient) TokenCount(_ context.Context, text string) (int, error) {
	c.tokenizeCalls = append(c.tokenizeCalls, text)
	if c.tokenizeErr != nil {
		return 0, c.tokenizeErr
	}
	if c.tokenCount != nil {
		return c.tokenCount(text), nil
	}
	return len([]rune(text)), nil
}

func (c *stubCohereRerankClient) Rerank(_ context.Context, query string, documents []string) ([]int, error) {
	c.calls = append(c.calls, rerankClientCall{
		query:     query,
		documents: slices.Clone(documents),
	})
	if c.err != nil {
		return nil, c.err
	}

	callIndex := len(c.calls) - 1
	if callIndex < len(c.results) {
		return c.results[callIndex], nil
	}

	indices := make([]int, len(documents))
	for i := range indices {
		indices[i] = i
	}
	return indices, nil
}

type stubRerankSecretManager struct {
	secret string
	err    error
}

func (m stubRerankSecretManager) GetSecret(string) (string, error) {
	return m.secret, m.err
}

func (m stubRerankSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.EnvSecretManagerType
}

func TestGetReranker(t *testing.T) {
	tests := []struct {
		name    string
		manager secret_manager.SecretManager
		enabled bool
		wantErr string
	}{
		{
			name: "disabled without a secret manager",
		},
		{
			name: "disabled when key is missing",
			manager: stubRerankSecretManager{
				err: fmt.Errorf("%w: Cohere key", secret_manager.ErrSecretNotFound),
			},
		},
		{
			name:    "disabled for an empty key",
			manager: stubRerankSecretManager{secret: "   "},
		},
		{
			name:    "enabled with a key",
			manager: stubRerankSecretManager{secret: "cohere-key"},
			enabled: true,
		},
		{
			name:    "returns secret retrieval errors",
			manager: stubRerankSecretManager{err: errors.New("secret store unavailable")},
			wantErr: "secret store unavailable",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reranker, err := GetReranker(tt.manager)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, reranker)
				return
			}
			require.NoError(t, err)
			if tt.enabled {
				assert.NotNil(t, reranker)
			} else {
				assert.Nil(t, reranker)
			}
		})
	}
}

func TestCohereRerankerReranksCandidateWindow(t *testing.T) {
	documents := make([]string, rerankCandidateLimit+2)
	indices := make([]int, rerankCandidateLimit)
	for i := range documents {
		documents[i] = fmt.Sprintf("document-%02d", i)
	}
	for i := range indices {
		indices[i] = len(indices) - i - 1
	}

	client := &stubCohereRerankClient{results: [][]int{indices}}
	reranker := newCohereRerankerWithClient(client)

	got, err := reranker.Rerank(context.Background(), "relevant query", documents)

	require.NoError(t, err)
	require.Len(t, client.calls, 1)
	assert.Equal(t, documents[:rerankCandidateLimit], client.calls[0].documents)
	assert.Equal(t, documents[rerankCandidateLimit-1], got[0])
	assert.Equal(t, documents[0], got[rerankCandidateLimit-1])
	assert.Equal(t, documents[rerankCandidateLimit:], got[rerankCandidateLimit:])
}

func TestCohereRerankerFusesChunkRankings(t *testing.T) {
	query := strings.Repeat("a", cohereRerankV4MaxTokens+1)
	documents := []string{"first", "second", "third"}
	client := &stubCohereRerankClient{
		tokenCount: func(text string) int {
			return len([]rune(text))
		},
		results: [][]int{
			{0, 1, 2},
			{1, 2, 0},
		},
	}
	reranker := newCohereRerankerWithClient(client)

	got, err := reranker.Rerank(context.Background(), query, documents)

	require.NoError(t, err)
	require.Len(t, client.calls, 2)
	assert.Equal(t, query, client.calls[0].query+client.calls[1].query)
	for _, call := range client.calls {
		assert.LessOrEqual(t, client.tokenCount(call.query), cohereRerankV4MaxTokens)
	}
	assert.Equal(t, []string{"second", "first", "third"}, got)
}

func TestCohereRerankerSkipsEmptyInputs(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		documents []string
	}{
		{
			name:      "empty query",
			query:     " ",
			documents: []string{"a", "b"},
		},
		{
			name:      "empty documents",
			query:     "query",
			documents: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &stubCohereRerankClient{}
			reranker := newCohereRerankerWithClient(client)

			got, err := reranker.Rerank(context.Background(), tt.query, tt.documents)

			require.NoError(t, err)
			assert.Equal(t, tt.documents, got)
			assert.Empty(t, client.calls)
		})
	}
}

func TestCohereRerankerReturnsClientError(t *testing.T) {
	client := &stubCohereRerankClient{err: errors.New("request failed")}
	reranker := newCohereRerankerWithClient(client)

	_, err := reranker.Rerank(context.Background(), "query", []string{"a"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to rerank documents with Cohere")
	assert.ErrorContains(t, err, "request failed")
}

func TestCohereRerankerRejectsInvalidResults(t *testing.T) {
	tests := []struct {
		name    string
		indices []int
	}{
		{
			name:    "missing result",
			indices: []int{0},
		},
		{
			name:    "out of range index",
			indices: []int{0, 2},
		},
		{
			name:    "duplicate index",
			indices: []int{0, 0},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &stubCohereRerankClient{results: [][]int{tt.indices}}
			reranker := newCohereRerankerWithClient(client)

			_, err := reranker.Rerank(context.Background(), "query", []string{"a", "b"})

			require.Error(t, err)
		})
	}
}

func TestCohereReranker_Live(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where API keys are available.
	if common.IsActiveEnvNonLocal() {
		t.Skip("Skipping integration test; API keys are unavailable in non-local sidekick environments")
	}

	secrets := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})
	apiKey, err := secrets.GetSecret(cohereAPIKeyEnvironmentVariable)
	if errors.Is(err, secret_manager.ErrSecretNotFound) {
		t.Skipf("Skipping integration test; %s is not configured", cohereAPIKeyEnvironmentVariable)
	}
	require.NoError(t, err)

	reranker := NewCohereReranker(apiKey)
	documents := []string{
		"Bananas and apples are common fruits.",
		"Go interfaces describe behavior through method sets.",
		"Ocean tides are influenced by the moon.",
	}

	got, err := reranker.Rerank(
		context.Background(),
		"How do interfaces work in the Go programming language?",
		documents,
	)

	require.NoError(t, err)
	require.Len(t, got, len(documents))
	assert.Equal(t, documents[1], got[0])
	assert.ElementsMatch(t, documents, got)
}
func TestCohereRerankMaxQueryTokens(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 32*1024, cohereRerankMaxQueryTokens("rerank-v4.0-pro"))
	assert.Equal(t, 4*1024, cohereRerankMaxQueryTokens("rerank-v3.5"))
	assert.Equal(t, 4*1024, cohereRerankMaxQueryTokens("rerank-english-v2.0"))
}
func TestSplitRerankQueryUsesModelTokenCounts(t *testing.T) {
	t.Parallel()

	query := strings.Repeat("界", 20000)
	client := &stubCohereRerankClient{
		tokenCount: func(text string) int {
			return len([]rune(text)) * 2
		},
	}

	chunks, err := splitRerankQuery(context.Background(), client, query)

	require.NoError(t, err)
	require.Len(t, chunks, 2)
	for _, chunk := range chunks {
		assert.LessOrEqual(t, client.tokenCount(chunk), cohereRerankV4MaxTokens)
	}
	assert.Equal(t, query, strings.Join(chunks, ""))
}
func TestCohereRerankerReturnsTokenizeError(t *testing.T) {
	client := &stubCohereRerankClient{tokenizeErr: errors.New("tokenize failed")}
	reranker := newCohereRerankerWithClient(client)

	_, err := reranker.Rerank(context.Background(), "query", []string{"document"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to split rerank query")
	assert.ErrorContains(t, err, "tokenize failed")
	assert.Empty(t, client.calls)
}
