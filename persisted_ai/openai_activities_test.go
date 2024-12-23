package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCachedEmbedActivity_AllCached(t *testing.T) {
	db := newTestRedisDatabase()

	oa := &OpenAIActivities{
		DatabaseAccessor: db,
		Embedder:         &MockOpenAIEmbedder{},
	}
	options := OpenAIEmbedActivityOptions{
		WorkspaceId:   "test_workspace",
		EmbeddingType: "ada2",
		ContentType:   "test_type",
		Subkeys:       []uint64{1},
	}

	expectedKeys := []string{"test_workspace:embedding:ada2:test_type:1"}
	expectedEmbedding := embedding.EmbeddingVector{0.5, 0.1}

	// Pre-cache the embeddings
	err := db.MSet(context.Background(), map[string]interface{}{expectedKeys[0]: expectedEmbedding})
	require.NoError(t, err)
	err = oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)

	// Check if the expected keys are in the cache and if their values are the expected embeddings.
	for _, key := range expectedKeys {
		value, err := db.Client.Get(context.Background(), key).Result()
		require.NoError(t, err)
		var embedding embedding.EmbeddingVector
		err = embedding.UnmarshalBinary([]byte(value))
		require.NoError(t, err)
		require.Equal(t, expectedEmbedding, embedding)
	}
}

func TestCachedEmbedActivity_NoKeys(t *testing.T) {
	db := newTestRedisDatabase()

	oa := &OpenAIActivities{
		DatabaseAccessor: db,
		Embedder:         &MockOpenAIEmbedder{},
	}
	options := OpenAIEmbedActivityOptions{
		WorkspaceId:   "test_workspace",
		EmbeddingType: "ada2",
		ContentType:   "test_type",
		Subkeys:       []uint64{},
	}

	err := oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)
}

func TestCachedEmbedActivity_MissedCache(t *testing.T) {
	db := newTestRedisDatabase()

	oa := &OpenAIActivities{
		DatabaseAccessor: db,
		Embedder:         &MockOpenAIEmbedder{},
	}
	options := OpenAIEmbedActivityOptions{
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.MockSecretManager{},
		},
		WorkspaceId:   "test_workspace",
		EmbeddingType: "ada2",
		ContentType:   "test_type",
		Subkeys:       []uint64{1, 2, 3},
	}

	// have all content keys
	err := db.MSet(context.Background(), map[string]interface{}{
		"test_workspace:test_type:1": "some test content 1",
		"test_workspace:test_type:2": "some test content 2",
		"test_workspace:test_type:3": "some test content 3",
	})
	require.NoError(t, err)

	// Pre-cache the embeddings for some of the keys
	err = db.MSet(context.Background(), map[string]interface{}{
		"test_workspace:test_type:1:ada2": embedding.EmbeddingVector{1.0, 2.0, 3.0},
		"test_workspace:test_type:2:ada2": embedding.EmbeddingVector{4.0, 5.0, 6.0},
	})
	require.NoError(t, err)

	err = oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)

	// Check if all the expected keys are in the cache and if their values are the expected embeddings.
	for _, subKey := range options.Subkeys {
		key := fmt.Sprintf("%s:%s:%d:ada2", options.WorkspaceId, options.EmbeddingType, subKey)
		exists, err := db.Client.Exists(context.Background(), key).Result()
		require.NoError(t, err)
		if exists > 0 {
			value, err := db.Client.Get(context.Background(), key).Result()
			require.NoError(t, err)
			require.NotNil(t, value)
		}
	}
}

type MockOpenAIEmbedder struct{}

func (m *MockOpenAIEmbedder) Embed(ctx context.Context, embeddingType string, secretManager secret_manager.SecretManager, texts []string) ([]embedding.EmbeddingVector, error) {
	return []embedding.EmbeddingVector{{1.0, 2.0, 3.0}}, nil
}
