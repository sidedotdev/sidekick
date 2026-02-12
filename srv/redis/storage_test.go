package redis

import (
	"context"
	"testing"

	"github.com/kelindar/binary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeletePrefix(t *testing.T) {
	ctx := context.Background()
	db := newTestRedisStorage()
	workspaceID := "test-workspace"

	t.Run("DeletePrefix removes matching keys", func(t *testing.T) {
		// Set up keys with different prefixes
		err := db.MSet(ctx, workspaceID, map[string]interface{}{
			"flow1:msg:block1": "value1",
			"flow1:msg:block2": "value2",
			"flow1:msg:block3": "value3",
			"flow2:msg:block1": "other-value",
			"unrelated-key":    "keep-me",
		})
		require.NoError(t, err)

		// Delete keys with flow1:msg: prefix
		err = db.DeletePrefix(ctx, workspaceID, "flow1:msg:")
		require.NoError(t, err)

		// Verify flow1:msg: keys are deleted
		results, err := db.MGet(ctx, workspaceID, []string{
			"flow1:msg:block1",
			"flow1:msg:block2",
			"flow1:msg:block3",
		})
		require.NoError(t, err)
		for _, result := range results {
			assert.Nil(t, result, "flow1:msg: keys should be deleted")
		}

		// Verify other keys still exist
		results, err = db.MGet(ctx, workspaceID, []string{"flow2:msg:block1", "unrelated-key"})
		require.NoError(t, err)

		var val1, val2 string
		require.NoError(t, binary.Unmarshal(results[0], &val1))
		require.NoError(t, binary.Unmarshal(results[1], &val2))
		assert.Equal(t, "other-value", val1)
		assert.Equal(t, "keep-me", val2)
	})

	t.Run("DeletePrefix with no matching keys", func(t *testing.T) {
		err := db.DeletePrefix(ctx, workspaceID, "nonexistent-prefix:")
		assert.NoError(t, err)
	})

	t.Run("DeletePrefix is workspace-scoped", func(t *testing.T) {
		otherWorkspace := "other-workspace"

		// Set up keys in both workspaces
		err := db.MSet(ctx, workspaceID, map[string]interface{}{
			"shared:key1": "ws1-value",
		})
		require.NoError(t, err)
		err = db.MSet(ctx, otherWorkspace, map[string]interface{}{
			"shared:key1": "ws2-value",
		})
		require.NoError(t, err)

		// Delete from first workspace only
		err = db.DeletePrefix(ctx, workspaceID, "shared:")
		require.NoError(t, err)

		// Verify first workspace key is deleted
		results, err := db.MGet(ctx, workspaceID, []string{"shared:key1"})
		require.NoError(t, err)
		assert.Nil(t, results[0])

		// Verify second workspace key still exists
		results, err = db.MGet(ctx, otherWorkspace, []string{"shared:key1"})
		require.NoError(t, err)
		assert.NotNil(t, results[0])
	})
}
