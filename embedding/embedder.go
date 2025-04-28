package embedding

import (
	"context"
	"sidekick/common"
	"sidekick/secret_manager"
)

// TaskType constants for Google Gemini API embeddings.
const (
	TaskTypeRetrievalDocument  = "RETRIEVAL_DOCUMENT"
	TaskTypeRetrievalQuery     = "RETRIEVAL_QUERY"
	TaskTypeCodeRetrievalQuery = "CODE_RETRIEVAL_QUERY"
	TaskTypeSemanticSimilarity = "SEMANTIC_SIMILARITY"
	TaskTypeClassification     = "CLASSIFICATION"
	TaskTypeClustering         = "CLUSTERING"
)

type Embedder interface {
	Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string, taskType string) ([]EmbeddingVector, error)
}
