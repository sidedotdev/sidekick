package persisted_ai

import (
	"os"
	"sidekick/embedding"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingCacheSetWritesReadableCacheEntry(t *testing.T) {
	t.Parallel()

	cache := &embeddingCache{
		cacheDir: t.TempDir(),
		model:    "test-model",
	}

	text := "File: example.go\n@@ -1 +1 @@\n-func old() {}\n+func new() {}"
	taskType := "retrieval_document"
	expected := embedding.EmbeddingVector{0.25, 0.5, 0.75}

	require.NoError(t, cache.set(text, taskType, expected))

	actual, ok, err := cache.get(text, taskType)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, expected, actual)
}

func TestEmbeddingCacheSetFailureLeavesExistingEntryReadable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can bypass directory write permissions")
	}

	cacheDir := t.TempDir()
	cache := &embeddingCache{
		cacheDir: cacheDir,
		model:    "test-model",
	}

	text := "diff chunk"
	taskType := "retrieval_document"
	original := embedding.EmbeddingVector{1, 2, 3}

	require.NoError(t, cache.set(text, taskType, original))

	require.NoError(t, os.Chmod(cacheDir, 0555))
	t.Cleanup(func() {
		_ = os.Chmod(cacheDir, 0755)
	})

	err := cache.set(text, taskType, embedding.EmbeddingVector{4, 5, 6})
	require.Error(t, err)

	actual, ok, err := cache.get(text, taskType)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, original, actual)
}
