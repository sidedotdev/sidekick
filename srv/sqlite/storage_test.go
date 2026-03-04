package sqlite

import (
	"bytes"
	"context"
	encoding_binary "encoding/binary"
	"errors"
	"testing"

	"github.com/kelindar/binary"
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

		assert.Len(t, results, 1)
		var val1 string
		require.NoError(t, binary.Unmarshal(results[0], &val1))
		assert.Equal(t, "value1", val1)
	})

	t.Run("MSet and MGet multiple key-value pairs", func(t *testing.T) {
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"key2": 42,
			"key3": true,
		})
		require.NoError(t, err)

		results, err := storage.MGet(ctx, workspaceID, []string{"key2", "key3"})
		require.NoError(t, err)
		assert.Len(t, results, 2)

		var val2 int
		var val3 bool
		require.NoError(t, binary.Unmarshal(results[0], &val2))
		require.NoError(t, binary.Unmarshal(results[1], &val3))
		assert.Equal(t, 42, val2)
		assert.Equal(t, true, val3)
	})

	t.Run("MGet non-existent keys", func(t *testing.T) {
		results, err := storage.MGet(ctx, workspaceID, []string{"non-existent"})
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, [][]byte{nil}, results)
	})

	t.Run("MGet mixed existing and non-existent keys", func(t *testing.T) {
		results, err := storage.MGet(ctx, workspaceID, []string{"key3", "non-existent", "key2"})
		require.NoError(t, err)
		assert.Len(t, results, 3)

		var val3 bool
		require.NoError(t, binary.Unmarshal(results[0], &val3))
		assert.Equal(t, true, val3)

		assert.Nil(t, results[1])

		var val2 int
		require.NoError(t, binary.Unmarshal(results[2], &val2))
		assert.Equal(t, 42, val2)
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

	t.Run("MSet and MGet an EmbeddingVector", func(t *testing.T) {
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"key1": embeddingVector{1.0, 2.0, 3.0},
		})
		require.NoError(t, err)

		results, err := storage.MGet(ctx, workspaceID, []string{"key1"})
		require.NoError(t, err)
		assert.Len(t, results, 1)

		var ev embeddingVector
		binary.Unmarshal(results[0], &ev)

		assert.Equal(t, embeddingVector{1.0, 2.0, 3.0}, ev)
	})
}

// NOTE: this is a copy of the EmbeddingVector type from embedding/embedding_vector.go
type embeddingVector []float32

func (ev embeddingVector) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	err := encoding_binary.Write(&buf, encoding_binary.LittleEndian, ev)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (ev *embeddingVector) UnmarshalBinary(data []byte) error {
	// Determine the number of float32 elements in the data
	if len(data)%4 != 0 {
		return errors.New("data length is not a multiple of 4")
	}
	numElements := len(data) / 4

	// Make sure ev has the correct length and capacity
	*ev = make(embeddingVector, numElements)

	// Now unmarshal
	buf := bytes.NewBuffer(data)
	err := encoding_binary.Read(buf, encoding_binary.LittleEndian, ev)
	if err != nil {
		return err
	}
	return nil
}

func TestDeletePrefix(t *testing.T) {
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "test_delete_prefix")
	workspaceID := "test-workspace"

	t.Run("DeletePrefix removes matching keys", func(t *testing.T) {
		// Set up keys with different prefixes
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"flow1:msg:block1": "value1",
			"flow1:msg:block2": "value2",
			"flow1:msg:block3": "value3",
			"flow2:msg:block1": "other-value",
			"unrelated-key":    "keep-me",
		})
		require.NoError(t, err)

		// Delete keys with flow1:msg: prefix
		err = storage.DeletePrefix(ctx, workspaceID, "flow1:msg:")
		require.NoError(t, err)

		// Verify flow1:msg: keys are deleted
		results, err := storage.MGet(ctx, workspaceID, []string{
			"flow1:msg:block1",
			"flow1:msg:block2",
			"flow1:msg:block3",
		})
		require.NoError(t, err)
		for _, result := range results {
			assert.Nil(t, result, "flow1:msg: keys should be deleted")
		}

		// Verify other keys still exist
		results, err = storage.MGet(ctx, workspaceID, []string{"flow2:msg:block1", "unrelated-key"})
		require.NoError(t, err)
		assert.NotNil(t, results[0], "flow2:msg:block1 should still exist")
		assert.NotNil(t, results[1], "unrelated-key should still exist")
	})

	t.Run("DeletePrefix with no matching keys", func(t *testing.T) {
		err := storage.DeletePrefix(ctx, workspaceID, "nonexistent-prefix:")
		assert.NoError(t, err)
	})

	t.Run("DeletePrefix is workspace-scoped", func(t *testing.T) {
		otherWorkspace := "other-workspace"

		// Set up keys in both workspaces
		err := storage.MSet(ctx, workspaceID, map[string]interface{}{
			"shared:key1": "ws1-value",
		})
		require.NoError(t, err)
		err = storage.MSet(ctx, otherWorkspace, map[string]interface{}{
			"shared:key1": "ws2-value",
		})
		require.NoError(t, err)

		// Delete from first workspace only
		err = storage.DeletePrefix(ctx, workspaceID, "shared:")
		require.NoError(t, err)

		// Verify first workspace key is deleted
		results, err := storage.MGet(ctx, workspaceID, []string{"shared:key1"})
		require.NoError(t, err)
		assert.Nil(t, results[0])

		// Verify second workspace key still exists
		results, err = storage.MGet(ctx, otherWorkspace, []string{"shared:key1"})
		require.NoError(t, err)
		assert.NotNil(t, results[0])
	})
}
