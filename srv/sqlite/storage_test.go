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
