package common

import (
	"os"
	"path/filepath"
	"sort"
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
	sort.Strings(paths)
	return paths
}

func TestWalkCodeDirectory_MultiLevelIgnores(t *testing.T) {
	tempDir := t.TempDir()
	setupGitRepo(t, tempDir)

	// Create nested directory structure
	createDir(t, filepath.Join(tempDir, "level1"))
	createDir(t, filepath.Join(tempDir, "level1/level2"))
	createDir(t, filepath.Join(tempDir, "level1/level2/level3"))

	// Create files at each level
	createFile(t, filepath.Join(tempDir, "root.txt"), "root")
	createFile(t, filepath.Join(tempDir, "level1/a.txt"), "a")
	createFile(t, filepath.Join(tempDir, "level1/level2/b.txt"), "b")
	createFile(t, filepath.Join(tempDir, "level1/level2/level3/c.txt"), "c")

	// Create ignore files at different levels
	createFile(t, filepath.Join(tempDir, ".gitignore"), "*.log")
	createFile(t, filepath.Join(tempDir, ".sideignore"), ".gitignore\n.ignore\n.sideignore")
	createFile(t, filepath.Join(tempDir, "level1/.ignore"), "*.txt")
	createFile(t, filepath.Join(tempDir, "level1/level2/.sideignore"), "!b.txt")

	// Create some files that should be ignored
	createFile(t, filepath.Join(tempDir, "root.log"), "log")
	createFile(t, filepath.Join(tempDir, "level1/a.log"), "log")

	paths := collectWalkedFiles(t, tempDir)
	assert.Equal(t, []string{
		"level1",
		"level1/level2",
		"level1/level2/b.txt",
		"level1/level2/level3",
		"root.txt",
	}, paths)
}

func TestWalkCodeDirectory_UnignorePatterns(t *testing.T) {
	tempDir := t.TempDir()
	setupGitRepo(t, tempDir)

	// Create test files
	createFile(t, filepath.Join(tempDir, "a.txt"), "a")
	createFile(t, filepath.Join(tempDir, "b.txt"), "b")
	createFile(t, filepath.Join(tempDir, "important.txt"), "important")
	createDir(t, filepath.Join(tempDir, "docs"))
	createFile(t, filepath.Join(tempDir, "docs/doc1.txt"), "doc1")
	createFile(t, filepath.Join(tempDir, "docs/important.txt"), "important doc")

	// Create ignore files with un-ignore patterns
	createFile(t, filepath.Join(tempDir, ".gitignore"), "*.txt")
	createFile(t, filepath.Join(tempDir, ".sideignore"), `
.gitignore
.ignore
.sideignore
!important.txt
docs/important.txt`)

	paths := collectWalkedFiles(t, tempDir)
	assert.Equal(t, []string{
		"docs",
		"important.txt",
	}, paths)
}

func TestWalkCodeDirectory_MixedScenarios(t *testing.T) {
	tempDir := t.TempDir()
	setupGitRepo(t, tempDir)

	// Create complex directory structure
	createDir(t, filepath.Join(tempDir, "src"))
	createDir(t, filepath.Join(tempDir, "src/pkg"))
	createDir(t, filepath.Join(tempDir, "tests"))

	// Create various files
	createFile(t, filepath.Join(tempDir, "src/main.go"), "main")
	createFile(t, filepath.Join(tempDir, "src/main_test.go"), "test")
	createFile(t, filepath.Join(tempDir, "src/pkg/util.go"), "util")
	createFile(t, filepath.Join(tempDir, "src/pkg/util_test.go"), "util test")
	createFile(t, filepath.Join(tempDir, "tests/integration.go"), "integration")
	createFile(t, filepath.Join(tempDir, "tests/bench.go"), "bench")

	// Create different types of ignore files
	createFile(t, filepath.Join(tempDir, ".gitignore"), `
*.log
*.tmp`)
	createFile(t, filepath.Join(tempDir, ".ignore"), `
.gitignore
.ignore
.sideignore
tests/
*_test.go`)
	createFile(t, filepath.Join(tempDir, "src/.sideignore"), `
!*_test.go
bench.go`)

	paths := collectWalkedFiles(t, tempDir)
	assert.Equal(t, []string{
		"src",
		"src/main.go",
		"src/main_test.go",
		"src/pkg",
		"src/pkg/util.go",
		"src/pkg/util_test.go",
	}, paths)
}

func TestWalkCodeDirectory_NoGitRepo(t *testing.T) {
	tempDir := t.TempDir()

	// Create files and directories
	createDir(t, filepath.Join(tempDir, "code"))
	createFile(t, filepath.Join(tempDir, "code/file1.go"), "file1")
	createFile(t, filepath.Join(tempDir, "code/file2.go"), "file2")
	createFile(t, filepath.Join(tempDir, "code/temp.log"), "log")

	// Create ignore file in the base directory
	createFile(t, filepath.Join(tempDir, "code/.ignore"), "*.log")

	paths := collectWalkedFiles(t, filepath.Join(tempDir, "code"))
	assert.Equal(t, []string{
		".ignore",
		"file1.go",
		"file2.go",
	}, paths)

	// Verify that ignore files in parent directories are still processed
	createFile(t, filepath.Join(tempDir, ".ignore"), "*.go")
	paths = collectWalkedFiles(t, filepath.Join(tempDir, "code"))
	assert.Equal(t, []string{
		".ignore",
	}, paths)
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
				".gitignore",
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
				".ignore",
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
				".sideignore",
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

func TestWalkCodeDirectory_UnignoreDirectory(t *testing.T) {
	tempDir := t.TempDir()
	setupGitRepo(t, tempDir)

	// Create a directory that will be ignored by .gitignore but un-ignored by .sideignore
	createDir(t, filepath.Join(tempDir, "vendor"))
	createDir(t, filepath.Join(tempDir, "vendor/pkg"))
	createFile(t, filepath.Join(tempDir, "vendor/file1.go"), "package vendor")
	createFile(t, filepath.Join(tempDir, "vendor/pkg/file2.go"), "package pkg")
	createFile(t, filepath.Join(tempDir, "main.go"), "package main")

	// .gitignore ignores the vendor directory
	createFile(t, filepath.Join(tempDir, ".gitignore"), "vendor/\n")

	// .sideignore un-ignores the vendor directory and all its contents
	createFile(t, filepath.Join(tempDir, ".sideignore"), ".gitignore\n.sideignore\n!vendor/\n!vendor/*\n!vendor/**/*\n")

	paths := collectWalkedFiles(t, tempDir)

	// We expect to see the vendor directory AND its contents
	expected := []string{
		"main.go",
		"vendor",
		"vendor/file1.go",
		"vendor/pkg",
		"vendor/pkg/file2.go",
	}
	assert.Equal(t, expected, paths)
}

func TestWalkCodeDirectory_IgnoreFilePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	setupGitRepo(t, tmpDir)

	// Create nested directories
	subDir := filepath.Join(tmpDir, "sub")
	subSubDir := filepath.Join(subDir, "subsub")
	createDir(t, subSubDir)

	// Create ignore files at different levels
	createFile(t, filepath.Join(tmpDir, ".sideignore"), ".gitignore\n.ignore\n.sideignore")
	createFile(t, filepath.Join(tmpDir, ".gitignore"), "*.txt\n")
	createFile(t, filepath.Join(subDir, ".gitignore"), "!important.txt\n")
	createFile(t, filepath.Join(subDir, ".ignore"), "*.go\n")
	createFile(t, filepath.Join(subSubDir, ".sideignore"), "!special.go\n.sideignore\n")

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
