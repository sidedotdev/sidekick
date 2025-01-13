package embedding

import (
	"context"
	"sidekick/common"
	"sidekick/secret_manager"
)

type Embedder interface {
	Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string) ([]EmbeddingVector, error)
}
