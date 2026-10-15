package persisted_ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRagIntegrationStorageIsolatedAndPersistent(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	ctx := context.Background()
	var workspaceID, repoRoot string
	cachedEmbedding := []byte("cached integration embedding")

	require.True(t, t.Run("first run", func(t *testing.T) {
		storage, id, root := setupTestWorkspace(t, ctx, dataHome)
		workspaceID, repoRoot = id, root
		require.NotEmpty(t, workspaceID)
		require.NotEmpty(t, repoRoot)
		service := setupRagService(storage)
		require.NoError(t, service.DatabaseAccessor.MSetRaw(ctx, workspaceID,
			map[string][]byte{"embedding-probe": cachedEmbedding}))
	}))

	require.True(t, t.Run("subsequent run", func(t *testing.T) {
		storage, id, root := setupTestWorkspace(t, ctx, dataHome)
		require.Equal(t, workspaceID, id)
		require.Equal(t, repoRoot, root)
		service := setupRagService(storage)
		cached, err := service.DatabaseAccessor.MGet(ctx, id, []string{"embedding-probe"})
		require.NoError(t, err)
		require.Equal(t, [][]byte{cachedEmbedding}, cached)

		workspaces, err := service.DatabaseAccessor.GetAllWorkspaces(ctx)
		require.NoError(t, err)
		require.Len(t, workspaces, 1)
		require.Equal(t, id, workspaces[0].Id)
	}))

	for _, name := range []string{"sidekick.core.db", "sidekick.kv.db"} {
		_, err := os.Stat(filepath.Join(dataHome, name))
		require.ErrorIs(t, err, os.ErrNotExist, "must not open the application database")
		require.FileExists(t, filepath.Join(dataHome, "integration-tests", "rag", name))
	}
}
