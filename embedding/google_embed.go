package embedding

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/secret_manager"

	"google.golang.org/genai"
)

const (
	/* NOTE: free tier of google doesn't yet support "gemini-embedding-exp-03-07"
	 * and limits are very low for that currently even with billing enabled (10
	 * RPM and 1000 RPD) */
	//GoogleDefaultModel = "gemini-embedding-exp-03-07" // NOTE: called "text-embedding-large-exp-03-07" in Vertex AI before GA
	GoogleDefaultModel = "text-embedding-004"
)

type GoogleEmbedder struct{}

func (ge GoogleEmbedder) Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string, taskType string) ([]EmbeddingVector, error) {
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

	contents := make([]*genai.Content, len(inputs))
	for i, text := range inputs {
		contents[i] = &genai.Content{
			Parts: []*genai.Part{{Text: text}},
		}
	}

	var embedConfig *genai.EmbedContentConfig
	if taskType != "" {
		embedConfig = &genai.EmbedContentConfig{TaskType: taskType}
	}

	res, err := client.Models.EmbedContent(ctx, model, contents, embedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embeddings: %w", err)
	}

	if len(res.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("unexpected number of embeddings returned: got %d, want %d", len(res.Embeddings), len(inputs))
	}

	results := make([]EmbeddingVector, 0, len(inputs))
	for _, embedding := range res.Embeddings {
		if len(embedding.Values) == 0 {
			return nil, fmt.Errorf("empty embedding values returned")
		}
		results = append(results, EmbeddingVector(embedding.Values))
	}

	return results, nil
}
