package embedding

import (
	"context"
	"fmt"
	"sidekick/secret_manager"

	openai "github.com/sashabaranov/go-openai"
)

type OpenAIEmbedder struct{}

var embeddingTypeToModel = map[string]openai.EmbeddingModel{
	"ada2":       openai.AdaEmbeddingV2,
	"oai-te3-sm": openai.SmallEmbedding3,
}

const ErrUnsupportedEmbeddingType = "unsupported embedding type"
const OpenaiApiKeySecretName = "OPENAI_API_KEY"

func (oe OpenAIEmbedder) Embed(ctx context.Context, embeddingType string, secretManager secret_manager.SecretManager, input []string) ([]EmbeddingVector, error) {
	token, err := secretManager.GetSecret(OpenaiApiKeySecretName)
	if err != nil {
		return nil, err
	}

	// TODO use this to dependency-inject the NewClient implementation: https://github.com/temporalio/samples-go/blob/a0c4ef5f372e854a472e4b634877e539a30cafe3/greetings/workflow.go#L22
	client := openai.NewClient(token)

	var model openai.EmbeddingModel
	model, ok := embeddingTypeToModel[embeddingType]
	if !ok {
		return nil, fmt.Errorf(ErrUnsupportedEmbeddingType + ": " + embeddingType)
	}

	response, err := client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: input,
		Model: model,
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
