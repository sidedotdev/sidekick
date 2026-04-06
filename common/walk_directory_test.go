package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupGitRepo initializes a git repository in the given directory
func setupGitRepo(t *testing.T, dir string) {
	t.Helper()
	err := os.Mkdir(filepath.Join(dir, ".git"), 0755)
	require.NoError(t, err)
}

// createFile creates a file with the given content
func createFile(t *testing.T, path string, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

// createDir creates a directory and any necessary parents
func createDir(t *testing.T, path string) {
	t.Helper()
	err := os.MkdirAll(path, 0755)
	require.NoError(t, err)
}

func TestFindGitRoot(t *testing.T) {
	t.Run("finds git root from subdirectory", func(t *testing.T) {
		tmpDir := t.TempDir()
		setupGitRepo(t, tmpDir)

		subDir := filepath.Join(tmpDir, "sub", "subsub")
		createDir(t, subDir)

		root, err := findGitRoot(subDir)
		require.NoError(t, err)
		assert.Equal(t, tmpDir, root)
	})

	t.Run("returns error when not in git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		_, err := findGitRoot(tmpDir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not in a git repository")
	})
}
