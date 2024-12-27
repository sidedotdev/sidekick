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

// collectWalkedFiles returns a slice of paths that WalkCodeDirectory visits
func collectWalkedFiles(t *testing.T, baseDir string) []string {
	var paths []string
	err := WalkCodeDirectory(baseDir, func(path string, entry os.DirEntry) error {
		relPath, err := filepath.Rel(baseDir, path)
		require.NoError(t, err)
		paths = append(paths, relPath)
		return nil
	})
	require.NoError(t, err)
	return paths
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

func TestWalkCodeDirectory_SingleIgnoreFile(t *testing.T) {
	tests := []struct {
		name       string
		ignoreType IgnoreFileType
		content    string
		files      []string
		expected   []string
	}{
		{
			name:       "gitignore",
			ignoreType: GitIgnoreType,
			content:    "*.txt\n",
			files: []string{
				"file.txt",
				"file.go",
				"sub/file.txt",
				"sub/file.go",
			},
			expected: []string{
				"file.go",
				"sub",
				"sub/file.go",
			},
		},
		{
			name:       "ignore",
			ignoreType: IgnoreType,
			content:    "*.go\n",
			files: []string{
				"file.txt",
				"file.go",
				"sub/file.txt",
				"sub/file.go",
			},
			expected: []string{
				"file.txt",
				"sub",
				"sub/file.txt",
			},
		},
		{
			name:       "sideignore",
			ignoreType: SideIgnoreType,
			content:    "sub/\n",
			files: []string{
				"file.txt",
				"file.go",
				"sub/file.txt",
				"sub/file.go",
			},
			expected: []string{
				"file.go",
				"file.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			setupGitRepo(t, tmpDir)

			// Create ignore file
			createFile(t, filepath.Join(tmpDir, tt.ignoreType.String()), tt.content)

			// Create test files
			for _, f := range tt.files {
				path := filepath.Join(tmpDir, f)
				if filepath.Ext(f) == "" {
					createDir(t, path)
				} else {
					createDir(t, filepath.Dir(path))
					createFile(t, path, "content")
				}
			}

			paths := collectWalkedFiles(t, tmpDir)
			assert.ElementsMatch(t, tt.expected, paths)
		})
	}
}

func TestWalkCodeDirectory_IgnoreFilePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	setupGitRepo(t, tmpDir)

	// Create nested directories
	subDir := filepath.Join(tmpDir, "sub")
	subSubDir := filepath.Join(subDir, "subsub")
	createDir(t, subSubDir)

	// Create ignore files at different levels
	createFile(t, filepath.Join(tmpDir, ".gitignore"), "*.txt\n")
	createFile(t, filepath.Join(subDir, ".gitignore"), "!important.txt\n")
	createFile(t, filepath.Join(subDir, ".ignore"), "*.go\n")
	createFile(t, filepath.Join(subSubDir, ".sideignore"), "!special.go\n")

	// Create test files
	files := []string{
		"file.txt",
		"file.go",
		"sub/file.txt",
		"sub/important.txt",
		"sub/file.go",
		"sub/subsub/file.txt",
		"sub/subsub/file.go",
		"sub/subsub/special.go",
	}

	for _, f := range files {
		path := filepath.Join(tmpDir, f)
		createFile(t, path, "content")
	}

	expected := []string{
		"file.go",
		"sub",
		"sub/important.txt",
		"sub/subsub",
		"sub/subsub/special.go",
	}

	paths := collectWalkedFiles(t, tmpDir)
	assert.ElementsMatch(t, expected, paths)
}
