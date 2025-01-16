package embedding

import (
	"context"
	"sidekick/common"
	"sidekick/secret_manager"
)

type MockEmbedder struct{}

func (m *MockEmbedder) Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, texts []string) ([]EmbeddingVector, error) {
	return []EmbeddingVector{{1.0, 2.0, 3.0}}, nil
}
