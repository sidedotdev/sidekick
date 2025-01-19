package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/embedding"
	"sidekick/secret_manager"
	"sidekick/srv/redis"
	"testing"

	"github.com/kelindar/binary"
	"github.com/stretchr/testify/require"
)

func TestCachedEmbedActivity_AllCached(t *testing.T) {
	storage := redis.NewTestRedisStorage()

	oa := &OpenAIActivities{
		Storage: storage,
	}
	options := OpenAIEmbedActivityOptions{
		WorkspaceId: "test_workspace",
		ModelConfig: common.ModelConfig{Model: "ada2", Provider: "mock"},
		ContentType: "test_type",
		Subkeys:     []uint64{1},
	}

	expectedKeys := []string{"embedding:ada2:test_type:1"}
	expectedEmbedding := embedding.EmbeddingVector{0.5, 0.1}

	// Pre-cache the embeddings
	err := storage.MSet(context.Background(), "test_workspace", map[string]interface{}{expectedKeys[0]: expectedEmbedding})
	require.NoError(t, err)
	err = oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)

	// Check if the expected keys are in the cache and if their values are the expected embeddings.
	for _, key := range expectedKeys {
		value, err := storage.Client.Get(context.Background(), options.WorkspaceId+":"+key).Result()
		require.NoError(t, err)
		var embedding embedding.EmbeddingVector
		err = binary.Unmarshal([]byte(value), &embedding)
		require.NoError(t, err)
		require.Equal(t, expectedEmbedding, embedding)
	}
}

func TestCachedEmbedActivity_NoKeys(t *testing.T) {
	db := redis.NewTestRedisStorage()

	oa := &OpenAIActivities{
		Storage: db,
	}
	options := OpenAIEmbedActivityOptions{
		WorkspaceId: "test_workspace",
		ModelConfig: common.ModelConfig{Model: "ada2", Provider: "mock"},
		ContentType: "test_type",
		Subkeys:     []uint64{},
	}

	err := oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)
}

func TestCachedEmbedActivity_MissedCache(t *testing.T) {
	storage := redis.NewTestRedisStorage()

	oa := &OpenAIActivities{
		Storage: storage,
	}
	options := OpenAIEmbedActivityOptions{
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.MockSecretManager{},
		},
		WorkspaceId: "test_workspace",
		ModelConfig: common.ModelConfig{Model: "ada2", Provider: "mock"},
		ContentType: "test_type",
		Subkeys:     []uint64{1, 2, 3},
	}

	// have all content keys
	err := storage.MSet(context.Background(), options.WorkspaceId, map[string]interface{}{
		"test_type:1": "some test content 1",
		"test_type:2": "some test content 2",
		"test_type:3": "some test content 3",
	})
	require.NoError(t, err)

	// Pre-cache the embeddings for some of the keys
	err = storage.MSet(context.Background(), options.WorkspaceId, map[string]interface{}{
		"embedding:ada2:test_type:1": embedding.EmbeddingVector{1.0, 2.0, 3.0},
		"embedding:ada2:test_type:2": embedding.EmbeddingVector{4.0, 5.0, 6.0},
	})
	require.NoError(t, err)

	err = oa.CachedEmbedActivity(context.Background(), options)
	require.NoError(t, err)

	// Check if all the expected keys are in the cache and if their values are the expected embeddings.
	for _, subKey := range options.Subkeys {
		key := fmt.Sprintf("%s:%s:%d:ada2", options.WorkspaceId, options.ModelConfig.Model, subKey)
		exists, err := storage.Client.Exists(context.Background(), key).Result()
		require.NoError(t, err)
		if exists > 0 {
			value, err := storage.Client.Get(context.Background(), key).Result()
			require.NoError(t, err)
			require.NotNil(t, value)
		}
	}
}
