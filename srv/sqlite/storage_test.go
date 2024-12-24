package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMGetAndMSet(t *testing.T) {
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "test_mget_mset")
	workspaceID := "test-workspace"

	t.Run("MSet and MGet single key-value pair", func(t *testing.T) {
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"key1": "value1",
		})
		require.NoError(t, err)

		results, err := storage.MGet(ctx, workspaceID, []string{"key1"})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"value1"}, results)
	})

	t.Run("MSet and MGet multiple key-value pairs", func(t *testing.T) {
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"key2": 42,
			"key3": true,
		})
		require.NoError(t, err)

		results, err := storage.MGet(ctx, workspaceID, []string{"key2", "key3"})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{float64(42), true}, results)
	})

	t.Run("MGet non-existent key", func(t *testing.T) {
		results, err := storage.MGet(ctx, workspaceID, []string{"non-existent"})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{nil}, results)
	})

	t.Run("MGet mixed existing and non-existent keys", func(t *testing.T) {
		results, err := storage.MGet(ctx, workspaceID, []string{"key1", "non-existent", "key2"})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"value1", nil, float64(42)}, results)
	})

	t.Run("MSet with empty input", func(t *testing.T) {
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{})
		require.NoError(t, err)
	})

	t.Run("MGet with empty input", func(t *testing.T) {
		results, err := storage.MGet(ctx, workspaceID, []string{})
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("MSet and MGet with complex data types", func(t *testing.T) {
		complexData := map[string]interface{}{
			"nested": map[string]interface{}{
				"array": []int{1, 2, 3},
				"object": map[string]string{
					"a": "b",
				},
			},
		}

		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"complex": complexData,
		})
		require.NoError(t, err)

		results, err := storage.MGet(ctx, workspaceID, []string{"complex"})
		require.NoError(t, err)
		require.Len(t, results, 1)

		retrievedData, ok := results[0].(map[string]interface{})
		require.True(t, ok, "Retrieved data should be a map[string]interface{}")

		nested, ok := retrievedData["nested"].(map[string]interface{})
		require.True(t, ok, "nested should be a map[string]interface{}")

		array, ok := nested["array"].([]interface{})
		require.True(t, ok, "array should be a []interface{}")
		assert.Equal(t, []interface{}{float64(1), float64(2), float64(3)}, array)

		object, ok := nested["object"].(map[string]interface{})
		require.True(t, ok, "object should be a map[string]interface{}")
		assert.Equal(t, "b", object["a"])
	})
}
