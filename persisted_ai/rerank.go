package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sidekick/common"
	"sidekick/secret_manager"

	cohere "github.com/cohere-ai/cohere-go/v2"
	cohereclient "github.com/cohere-ai/cohere-go/v2/client"
	"github.com/cohere-ai/cohere-go/v2/option"
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
	model  string
}

func (c cohereSDKRerankClient) TokenCount(ctx context.Context, text string) (int, error) {
	response, err := c.client.Tokenize(ctx, &cohere.TokenizeRequest{
		Model: c.model,
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
		Model:     cohere.String(c.model),
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
	model  string
}

// NewCohereReranker creates a reranker using the provided Cohere API key.
func NewCohereReranker(apiKey string) *CohereReranker {
	return newCohereReranker(apiKey, common.DefaultCohereRerankingModel, "")
}

// GetReranker returns nil when the selection or its credentials are unavailable.
func GetReranker(modelConfig common.ModelConfig, providers []common.ModelProviderPublicConfig, secretManager secret_manager.SecretManager) (Reranker, error) {
	if secretManager == nil || modelConfig.Provider == "" {
		return nil, nil
	}

	provider := common.ModelProviderPublicConfig{Type: modelConfig.Provider}
	found := false
	for _, candidate := range providers {
		name := candidate.Name
		if name == "" {
			name = candidate.Type
		}
		if name != modelConfig.Provider {
			continue
		}
		if found {
			return nil, fmt.Errorf("multiple providers found with name %s", name)
		}
		provider = candidate
		found = true
	}
	if provider.Type != "cohere" {
		return nil, fmt.Errorf("unsupported reranking provider: %s", provider.Type)
	}

	apiKey, err := secretManager.GetSecret(modelConfig.NormalizedProviderName() + "_API_KEY")
	if err != nil {
		if errors.Is(err, secret_manager.ErrSecretNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get API key for %s: %w", modelConfig.Provider, err)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, nil
	}
	model := modelConfig.Model
	if model == "" {
		model = common.DefaultCohereRerankingModel
	}
	return newCohereReranker(apiKey, model, provider.BaseURL), nil
}

func newCohereRerankerWithClient(client cohereRerankClient) *CohereReranker {
	return &CohereReranker{client: client, model: common.DefaultCohereRerankingModel}
}

// Rerank reranks at most the first rerankCandidateLimit documents. Documents
// outside that window retain their original order after the reranked candidates.
func (r *CohereReranker) Rerank(ctx context.Context, query string, documents []string) ([]string, error) {
	if r == nil || r.client == nil || len(documents) == 0 || strings.TrimSpace(query) == "" {
		return documents, nil
	}

	candidateCount := min(len(documents), rerankCandidateLimit)
	candidates := documents[:candidateCount]
	queryChunks, err := splitRerankQuery(ctx, r.client, query, r.model)
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

func splitRerankQuery(ctx context.Context, client cohereRerankClient, query, model string) ([]string, error) {
	runes := []rune(query)
	maxTokens := cohereRerankMaxQueryTokens(model)
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

func newCohereReranker(apiKey, model, baseURL string) *CohereReranker {
	options := []option.RequestOption{cohereclient.WithToken(apiKey)}
	if baseURL != "" {
		options = append(options, cohereclient.WithBaseURL(baseURL))
	}
	return &CohereReranker{
		model: model,
		client: cohereSDKRerankClient{
			client: cohereclient.NewClient(options...),
			model:  model,
		},
	}
}
