package persisted_ai

import (
	"context"
	"fmt"
	"log"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"sidekick/srv"
)

type OpenAIEmbedActivityOptions struct {
	Secrets       secret_manager.SecretManagerContainer
	WorkspaceId   string
	ContentType   string
	EmbeddingType string // FIXME make this an enum
	Subkeys       []uint64
}

type Embedder interface {
	Embed(ctx context.Context, embeddingType string, secretManager secret_manager.SecretManager, inputs []string) ([]embedding.EmbeddingVector, error)
}

type OpenAIActivities struct {
	FlowEventAccessor srv.FlowEventAccessor
	Storage           srv.Storage
	Embedder
}

/*
This activity embeds the content of the given subkeys and stores the embeddings
in the database. It doesn't return anything as embeddings are quite large and
don't make sense to pass over the temporal activity boundary, especially since
we already expect to cache these values in the database.
*/
// TODO move to embed package under EmbedActivities struct
func (oa *OpenAIActivities) CachedEmbedActivity(ctx context.Context, options OpenAIEmbedActivityOptions) error {
	contentKeys := make([]string, len(options.Subkeys))
	embeddingKeys := make([]string, len(options.Subkeys))
	for i, subKey := range options.Subkeys {
		contentKeys[i] = fmt.Sprintf("%s:%s:%d", options.WorkspaceId, options.ContentType, subKey)
		embeddingKeys[i] = fmt.Sprintf("%s:embedding:%s:%s:%d", options.WorkspaceId, options.EmbeddingType, options.ContentType, subKey)
	}

	var cachedEmbeddings []interface{}
	var err error
	if len(embeddingKeys) > 0 {
		cachedEmbeddings, err = oa.Storage.MGet(ctx, embeddingKeys)
		if err != nil {
			return err
		}
	}

	toEmbedContentKeys := make([]string, 0)
	missingEmbeddingKeys := make([]string, 0)
	for i, cachedEmbedding := range cachedEmbeddings {
		if cachedEmbedding == nil {
			toEmbedContentKeys = append(toEmbedContentKeys, contentKeys[i])
			missingEmbeddingKeys = append(missingEmbeddingKeys, embeddingKeys[i])
		}
	}

	// TODO replace with metric
	log.Printf("embedding %d keys\n", len(toEmbedContentKeys))
	if len(toEmbedContentKeys) > 0 {
		values, err := oa.Storage.MGet(ctx, toEmbedContentKeys)
		if err != nil {
			return err
		}
		var input []string

		for i, value := range values {
			if value == nil {
				return fmt.Errorf("missing value for content key: %s", toEmbedContentKeys[i])
			} else {
				text, ok := value.(string)
				if !ok {
					return fmt.Errorf("value for key %s is not a string: %v", toEmbedContentKeys[i], value)
				}
				input = append(input, text)
			}
		}

		cacheValues := make(map[string]interface{}, len(input))
		batchSize := 2048 // 2048 is the maximum batch size for the OpenAI embedding API
		for i := 0; i < len(input); i += batchSize {
			end := i + batchSize
			if end > len(input) {
				end = len(input)
			}
			embeddings, err := oa.Embedder.Embed(ctx, options.EmbeddingType, options.Secrets.SecretManager, input[i:end])
			if err != nil {
				return err
			}
			for i, embedding := range embeddings {
				cacheValues[missingEmbeddingKeys[i]] = embedding
			}
		}

		err = oa.Storage.MSet(ctx, cacheValues)
		if err != nil {
			return err
		}
	}
	return nil
}
