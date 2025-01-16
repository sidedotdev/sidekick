package embedding

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/secret_manager"

	openai "github.com/sashabaranov/go-openai"
)

const OpenaiDefaultModel = string(openai.SmallEmbedding3)

type OpenAIEmbedder struct {
	BaseURL string
}

func (oe OpenAIEmbedder) Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, input []string) ([]EmbeddingVector, error) {
	providerNameNormalized := modelConfig.NormalizedProviderName()
	token, err := secretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, err
	}

	// TODO use this to dependency-inject the NewClient implementation: https://github.com/temporalio/samples-go/blob/a0c4ef5f372e854a472e4b634877e539a30cafe3/greetings/workflow.go#L22

	clientConfig := openai.DefaultConfig(token)
	if oe.BaseURL != "" {
		clientConfig.BaseURL = oe.BaseURL
	}
	client := openai.NewClientWithConfig(clientConfig)

	model := modelConfig.Model
	if model == "" {
		model = OpenaiDefaultModel
	}

	response, err := client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: input,
		Model: openai.EmbeddingModel(model),
		User:  "",
	})
	if err != nil {
		return nil, err
	}
	embeddingVectors := make([]EmbeddingVector, len(response.Data))
	for i, embedding := range response.Data {
		embeddingVectors[i] = embedding.Embedding
	}
	return embeddingVectors, nil
}
