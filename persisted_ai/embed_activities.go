package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"sidekick/srv"

	"github.com/kelindar/binary"
	"github.com/rs/zerolog/log"
)

type CachedEmbedActivityOptions struct {
	Secrets            secret_manager.SecretManagerContainer
	WorkspaceId        string
	ContentType        string
	ModelConfig        common.ModelConfig
	Subkeys            []string
	AvailableProviders []common.ModelProviderPublicConfig
}

type EmbedActivities struct {
	Storage srv.Storage
}

/*
This activity embeds the content of the given subkeys and stores the embeddings
in the database. It doesn't return anything as embeddings are quite large and
don't make sense to pass over the temporal activity boundary, especially since
we already expect to cache these values in the database.
*/
// TODO move to embed package under EmbedActivities struct
func (ea *EmbedActivities) CachedEmbedActivity(ctx context.Context, options CachedEmbedActivityOptions) error {
	contentKeys := make([]string, len(options.Subkeys))
	embeddingKeys := make([]string, len(options.Subkeys))
	for i, subKey := range options.Subkeys {
		contentKeys[i] = fmt.Sprintf("%s:%s", options.ContentType, subKey)
		embeddingKey, err := constructEmbeddingKey(embeddingKeyOptions{
			provider:    options.ModelConfig.Provider,
			model:       options.ModelConfig.Model,
			contentType: options.ContentType,
			subKey:      subKey,
		})
		if err != nil {
			return err
		}
		embeddingKeys[i] = embeddingKey
	}

	var cachedEmbeddings [][]byte
	var err error
	if len(embeddingKeys) > 0 {
		cachedEmbeddings, err = ea.Storage.MGet(ctx, options.WorkspaceId, embeddingKeys)
		if err != nil {
			log.Error().Err(err).Msg("failed to get cached embeddings")
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
	log.Info().Msgf("embedding %d keys\n", len(toEmbedContentKeys))
	if len(toEmbedContentKeys) > 0 {
		values, err := ea.Storage.MGet(ctx, options.WorkspaceId, toEmbedContentKeys)
		if err != nil {
			log.Error().Err(err).Msg("failed to get cached embeddings")
			return err
		}
		var input []string

		for i, value := range values {
			if value == nil {
				return fmt.Errorf("missing value for content key: %s", toEmbedContentKeys[i])
			} else {
				var text string
				err := binary.Unmarshal(value, &text)
				if err != nil {
					return fmt.Errorf("value %v for key %s failed to unmarshal: %w", value, toEmbedContentKeys[i], err)
				}
				input = append(input, text)
			}
		}

		embedder, err := getEmbedder(options.ModelConfig)
		if err != nil {
			return err
		}
		cacheValues := make(map[string]interface{}, len(input))
		batchSize := 2048 // 2048 is the maximum batch size for the OpenAI embedding API
		for i := 0; i < len(input); i += batchSize {
			end := i + batchSize
			if end > len(input) {
				end = len(input)
			}
			embeddings, err := embedder.Embed(ctx, options.ModelConfig, options.Secrets.SecretManager, input[i:end], embedding.TaskTypeRetrievalDocument)
			if err != nil {
				return fmt.Errorf("failed to embed content: %w", err)
			}
			for j, embedding := range embeddings {
				cacheValues[missingEmbeddingKeys[i+j]] = embedding
			}
		}

		err = ea.Storage.MSet(ctx, options.WorkspaceId, cacheValues)
		if err != nil {
			return err
		}
	}
	return nil
}

type embeddingKeyOptions struct {
	provider    string
	model       string
	contentType string
	subKey      string
}

func constructEmbeddingKey(options embeddingKeyOptions) (string, error) {
	model := options.model
	if model == "" {
		switch options.provider {
		case "openai":
			{
				model = embedding.OpenaiDefaultModel
			}
		case "google":
			{
				model = embedding.GoogleDefaultModel
			}
		default:
			{
				return "", fmt.Errorf("No embedding model given for provider %s", options.provider)
			}
		}
	}
	return fmt.Sprintf("embedding:%s:%s:%s", options.model, options.contentType, options.subKey), nil
}
