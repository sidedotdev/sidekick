package embedding

import (
	"context"
	"sidekick/common"
	"sidekick/secret_manager"
)

// TaskType constants for Google Gemini API embeddings.
const (
	TaskTypeRetrievalDocument  = "retrieval_document"
	TaskTypeRetrievalQuery     = "retrieval_query"
	TaskTypeCodeRetrievalQuery = "code_retrieval_query"
	TaskTypeSemanticSimilarity = "semantic_similarity"
	TaskTypeClassification     = "classification"
	TaskTypeClustering         = "clustering"
)

type Embedder interface {
	Embed(ctx context.Context, modelConfig common.ModelConfig, secretManager secret_manager.SecretManager, inputs []string, taskType string) ([]EmbeddingVector, error)
}
