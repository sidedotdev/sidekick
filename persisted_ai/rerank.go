package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sidekick/secret_manager"

	cohere "github.com/cohere-ai/cohere-go/v2"
	cohereclient "github.com/cohere-ai/cohere-go/v2/client"
)

const (
	cohereAPIKeyEnvironmentVariable = "COHERE_API_KEY"
	cohereRerankModel               = "rerank-v4.0-pro"
	rerankCandidateLimit            = 50
	cohereRerankDefaultMaxTokens    = 4 * 1024
	cohereRerankV4MaxTokens         = 32 * 1024
	cohereTokenizeMaxChars          = 65536
)

// Reranker reorders documents by their relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]string, error)
}

type cohereRerankClient interface {
	TokenCount(ctx context.Context, text string) (int, error)
	Rerank(ctx context.Context, query string, documents []string) ([]int, error)
}

type cohereSDKRerankClient struct {
	client *cohereclient.Client
}

func (c cohereSDKRerankClient) TokenCount(ctx context.Context, text string) (int, error) {
	response, err := c.client.Tokenize(ctx, &cohere.TokenizeRequest{
		Model: cohereRerankModel,
		Text:  text,
	})
	if err != nil {
		return 0, err
	}
	return len(response.Tokens), nil
}

func (c cohereSDKRerankClient) Rerank(ctx context.Context, query string, documents []string) ([]int, error) {
	requestDocuments := make([]*cohere.RerankRequestDocumentsItem, len(documents))
	for i, document := range documents {
		requestDocuments[i] = &cohere.RerankRequestDocumentsItem{String: document}
	}

	topN := len(documents)
	response, err := c.client.Rerank(ctx, &cohere.RerankRequest{
		Model:     cohere.String(cohereRerankModel),
		Query:     query,
		Documents: requestDocuments,
		TopN:      &topN,
	})
	if err != nil {
		return nil, err
	}

	indices := make([]int, len(response.Results))
	for i, result := range response.Results {
		indices[i] = result.Index
	}
	return indices, nil
}

type CohereReranker struct {
	client cohereRerankClient
}

// NewCohereReranker creates a reranker using the provided Cohere API key.
func NewCohereReranker(apiKey string) *CohereReranker {
	return &CohereReranker{
		client: cohereSDKRerankClient{
			client: cohereclient.NewClient(cohereclient.WithToken(apiKey)),
		},
	}
}

// GetReranker returns nil when reranking is not configured.
func GetReranker(secretManager secret_manager.SecretManager) (Reranker, error) {
	if secretManager == nil {
		return nil, nil
	}

	apiKey, err := secretManager.GetSecret(cohereAPIKeyEnvironmentVariable)
	if err != nil {
		if errors.Is(err, secret_manager.ErrSecretNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Cohere API key: %w", err)
	}

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, nil
	}
	return NewCohereReranker(apiKey), nil
}

func newCohereRerankerWithClient(client cohereRerankClient) *CohereReranker {
	return &CohereReranker{client: client}
}

// Rerank reranks at most the first rerankCandidateLimit documents. Documents
// outside that window retain their original order after the reranked candidates.
func (r *CohereReranker) Rerank(ctx context.Context, query string, documents []string) ([]string, error) {
	if r == nil || r.client == nil || len(documents) == 0 || strings.TrimSpace(query) == "" {
		return documents, nil
	}

	candidateCount := min(len(documents), rerankCandidateLimit)
	candidates := documents[:candidateCount]
	queryChunks, err := splitRerankQuery(ctx, r.client, query)
	if err != nil {
		return nil, fmt.Errorf("failed to split rerank query: %w", err)
	}

	rankings := make([]WeightedRanking, 0, len(queryChunks))
	for _, queryChunk := range queryChunks {
		indices, err := r.client.Rerank(ctx, queryChunk, candidates)
		if err != nil {
			return nil, fmt.Errorf("failed to rerank documents with Cohere: %w", err)
		}
		if err := validateRerankIndices(indices, candidateCount); err != nil {
			return nil, err
		}

		rankedCandidateIDs := make([]string, len(indices))
		for i, index := range indices {
			rankedCandidateIDs[i] = strconv.Itoa(index)
		}
		rankings = append(rankings, WeightedRanking{
			Items:  rankedCandidateIDs,
			Weight: BaselineRankWeight,
		})
	}

	fusedCandidateIDs := FuseResults(rankings)
	result := make([]string, 0, len(documents))
	for _, candidateID := range fusedCandidateIDs {
		index, err := strconv.Atoi(candidateID)
		if err != nil {
			return nil, fmt.Errorf("invalid rerank candidate identifier %q: %w", candidateID, err)
		}
		result = append(result, candidates[index])
	}
	result = append(result, documents[candidateCount:]...)
	return result, nil
}

func validateRerankIndices(indices []int, candidateCount int) error {
	if len(indices) != candidateCount {
		return fmt.Errorf("Cohere rerank returned %d results for %d documents", len(indices), candidateCount)
	}

	seen := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		if index < 0 || index >= candidateCount {
			return fmt.Errorf("Cohere rerank returned invalid document index %d", index)
		}
		if _, ok := seen[index]; ok {
			return fmt.Errorf("Cohere rerank returned duplicate document index %d", index)
		}
		seen[index] = struct{}{}
	}
	return nil
}

func cohereRerankMaxQueryTokens(model string) int {
	if strings.HasPrefix(model, "rerank-v4.0-") {
		return cohereRerankV4MaxTokens
	}
	return cohereRerankDefaultMaxTokens
}

func splitRerankQuery(ctx context.Context, client cohereRerankClient, query string) ([]string, error) {
	runes := []rune(query)
	maxTokens := cohereRerankMaxQueryTokens(cohereRerankModel)
	chunks := make([]string, 0, len(runes)/maxTokens+1)

	for start := 0; start < len(runes); {
		low := start + 1
		high := min(start+cohereTokenizeMaxChars, len(runes))
		bestEnd := start

		for low <= high {
			mid := low + (high-low)/2
			candidate := string(runes[start:mid])
			tokenCount, err := client.TokenCount(ctx, candidate)
			if err != nil {
				return nil, fmt.Errorf("failed to tokenize query: %w", err)
			}

			if tokenCount <= maxTokens {
				bestEnd = mid
				low = mid + 1
			} else {
				high = mid - 1
			}
		}

		if bestEnd == start {
			return nil, fmt.Errorf("query character exceeds the %d-token context", maxTokens)
		}
		chunks = append(chunks, string(runes[start:bestEnd]))
		start = bestEnd
	}

	return chunks, nil
}
