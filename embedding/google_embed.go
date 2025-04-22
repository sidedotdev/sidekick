package embedding

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/secret_manager"

	"google.golang.org/genai"
)

const (
	GoogleDefaultModel = "gemini-embedding-exp-03-07"
	maxBatchSize      = 100
)

type GoogleEmbedder struct{}

func (ge GoogleEmbedder) Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string) ([]EmbeddingVector, error) {
	apiKey, err := secretManager.GetSecret("GOOGLE_API_KEY")
	if err != nil {
		return nil, fmt.Errorf("failed to get Google API key: %w", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	model := modelConfig.Model
	if model == "" {
		model = GoogleDefaultModel
	}

	var results []EmbeddingVector

	// Process in batches of maxBatchSize
	for i := 0; i < len(inputs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}

		batch := inputs[i:end]
		batchResults := make([]EmbeddingVector, 0, len(batch))

		contents := make([]*genai.Content, len(batch))
		for i, text := range batch {
			contents[i] = &genai.Content{
				Parts: []*genai.Part{{Text: text}},
			}
		}

		res, err := client.Models.EmbedContent(ctx, model, contents, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to generate embeddings for batch %d/%d: %w", i/maxBatchSize+1, (len(inputs)+maxBatchSize-1)/maxBatchSize, err)
		}

		if len(res.Embeddings) != len(batch) {
			return nil, fmt.Errorf("unexpected number of embeddings returned for batch %d/%d: got %d, want %d", 
				i/maxBatchSize+1, (len(inputs)+maxBatchSize-1)/maxBatchSize, len(res.Embeddings), len(batch))
		}

		for _, embedding := range res.Embeddings {
			if len(embedding.Values) == 0 {
				return nil, fmt.Errorf("empty embedding values returned in batch %d/%d", i/maxBatchSize+1, (len(inputs)+maxBatchSize-1)/maxBatchSize)
			}
			batchResults = append(batchResults, EmbeddingVector(embedding.Values))
		}

		results = append(results, batchResults...)
	}

	return results, nil
}