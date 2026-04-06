package env

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupWalkGitRepo(t *testing.T, dir string) {
	t.Helper()
	err := os.Mkdir(filepath.Join(dir, ".git"), 0755)
	require.NoError(t, err)
}

func createTestFile(t *testing.T, path string, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

func createTestDir(t *testing.T, path string) {
	t.Helper()
	err := os.MkdirAll(path, 0755)
	require.NoError(t, err)
}

func collectWalkedFiles(t *testing.T, baseDir string) []string {
	t.Helper()
	ctx := context.Background()
	localEnv := &LocalEnv{WorkingDirectory: baseDir}
	var paths []string
	err := localEnv.Walk(ctx, common.SidekickIgnoreFileNames, func(path string, isDir bool) error {
		relPath, err := filepath.Rel(baseDir, path)
		require.NoError(t, err)
		paths = append(paths, relPath)
		return nil
	})
	require.NoError(t, err)
	sort.Strings(paths)
	return paths
}

func TestWalk_MultiLevelIgnores(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	setupWalkGitRepo(t, tempDir)

	createTestDir(t, filepath.Join(tempDir, "level1"))
	createTestDir(t, filepath.Join(tempDir, "level1/level2"))
	createTestDir(t, filepath.Join(tempDir, "level1/level2/level3"))

	createTestFile(t, filepath.Join(tempDir, "root.txt"), "root")
	createTestFile(t, filepath.Join(tempDir, "level1/a.txt"), "a")
	createTestFile(t, filepath.Join(tempDir, "level1/level2/b.txt"), "b")
	createTestFile(t, filepath.Join(tempDir, "level1/level2/level3/c.txt"), "c")

	createTestFile(t, filepath.Join(tempDir, ".gitignore"), "*.log")
	createTestFile(t, filepath.Join(tempDir, ".sideignore"), ".gitignore\n.ignore\n.sideignore")
	createTestFile(t, filepath.Join(tempDir, "level1/.ignore"), "*.txt")
	createTestFile(t, filepath.Join(tempDir, "level1/level2/.sideignore"), "!b.txt")

	createTestFile(t, filepath.Join(tempDir, "root.log"), "log")
	createTestFile(t, filepath.Join(tempDir, "level1/a.log"), "log")

	paths := collectWalkedFiles(t, tempDir)
	assert.Equal(t, []string{
		"level1",
		"level1/level2",
		"level1/level2/b.txt",
		"level1/level2/level3",
		"root.txt",
	}, paths)
}

func TestWalk_UnignorePatterns(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	setupWalkGitRepo(t, tempDir)

	createTestFile(t, filepath.Join(tempDir, "a.txt"), "a")
	createTestFile(t, filepath.Join(tempDir, "b.txt"), "b")
	createTestFile(t, filepath.Join(tempDir, "important.txt"), "important")
	createTestDir(t, filepath.Join(tempDir, "docs"))
	createTestFile(t, filepath.Join(tempDir, "docs/doc1.txt"), "doc1")
	createTestFile(t, filepath.Join(tempDir, "docs/important.txt"), "important doc")

	createTestFile(t, filepath.Join(tempDir, ".gitignore"), "*.txt")
	createTestFile(t, filepath.Join(tempDir, ".sideignore"), `
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

func TestWalk_MixedScenarios(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	setupWalkGitRepo(t, tempDir)

	createTestDir(t, filepath.Join(tempDir, "src"))
	createTestDir(t, filepath.Join(tempDir, "src/pkg"))
	createTestDir(t, filepath.Join(tempDir, "tests"))

	createTestFile(t, filepath.Join(tempDir, "src/main.go"), "main")
	createTestFile(t, filepath.Join(tempDir, "src/main_test.go"), "test")
	createTestFile(t, filepath.Join(tempDir, "src/pkg/util.go"), "util")
	createTestFile(t, filepath.Join(tempDir, "src/pkg/util_test.go"), "util test")
	createTestFile(t, filepath.Join(tempDir, "tests/integration.go"), "integration")
	createTestFile(t, filepath.Join(tempDir, "tests/bench.go"), "bench")

	createTestFile(t, filepath.Join(tempDir, ".gitignore"), `
*.log
*.tmp`)
	createTestFile(t, filepath.Join(tempDir, ".ignore"), `
.gitignore
.ignore
.sideignore
tests/
*_test.go`)
	createTestFile(t, filepath.Join(tempDir, "src/.sideignore"), `
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

func TestWalk_NoGitRepo(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	createTestDir(t, filepath.Join(tempDir, "code"))
	createTestFile(t, filepath.Join(tempDir, "code/file1.go"), "file1")
	createTestFile(t, filepath.Join(tempDir, "code/file2.go"), "file2")
	createTestFile(t, filepath.Join(tempDir, "code/temp.log"), "log")

	createTestFile(t, filepath.Join(tempDir, "code/.ignore"), "*.log")

	paths := collectWalkedFiles(t, filepath.Join(tempDir, "code"))
	assert.Equal(t, []string{
		".ignore",
		"file1.go",
		"file2.go",
	}, paths)

	createTestFile(t, filepath.Join(tempDir, ".ignore"), "*.go")
	paths = collectWalkedFiles(t, filepath.Join(tempDir, "code"))
	assert.Equal(t, []string{
		".ignore",
	}, paths)
}

func TestWalk_SingleIgnoreFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		ignoreFileName string
		content        string
		files          []string
		expected       []string
	}{
		{
			name:           "gitignore",
			ignoreFileName: ".gitignore",
			content:        "*.txt\n",
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
			name:           "ignore",
			ignoreFileName: ".ignore",
			content:        "*.go\n",
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
			name:           "sideignore",
			ignoreFileName: ".sideignore",
			content:        "sub/\n",
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
			t.Parallel()
			tmpDir := t.TempDir()
			setupWalkGitRepo(t, tmpDir)

			createTestFile(t, filepath.Join(tmpDir, tt.ignoreFileName), tt.content)

			for _, f := range tt.files {
				path := filepath.Join(tmpDir, f)
				if filepath.Ext(f) == "" {
					createTestDir(t, path)
				} else {
					createTestDir(t, filepath.Dir(path))
					createTestFile(t, path, "content")
				}
			}

			ctx := context.Background()
			localEnv := &LocalEnv{WorkingDirectory: tmpDir}
			var paths []string
			err := localEnv.Walk(ctx, []string{tt.ignoreFileName}, func(path string, isDir bool) error {
				relPath, relErr := filepath.Rel(tmpDir, path)
				require.NoError(t, relErr)
				paths = append(paths, relPath)
				return nil
			})
			require.NoError(t, err)
			sort.Strings(paths)
			assert.ElementsMatch(t, tt.expected, paths)
		})
	}
}

func TestWalk_UnignoreDirectory(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	setupWalkGitRepo(t, tempDir)

	createTestDir(t, filepath.Join(tempDir, "vendor"))
	createTestDir(t, filepath.Join(tempDir, "vendor/pkg"))
	createTestFile(t, filepath.Join(tempDir, "vendor/file1.go"), "package vendor")
	createTestFile(t, filepath.Join(tempDir, "vendor/pkg/file2.go"), "package pkg")
	createTestFile(t, filepath.Join(tempDir, "main.go"), "package main")

	createTestFile(t, filepath.Join(tempDir, ".gitignore"), "vendor/\n")
	createTestFile(t, filepath.Join(tempDir, ".sideignore"), ".gitignore\n.sideignore\n!vendor/\n!vendor/*\n!vendor/**/*\n")

	paths := collectWalkedFiles(t, tempDir)

	expected := []string{
		"main.go",
		"vendor",
		"vendor/file1.go",
		"vendor/pkg",
		"vendor/pkg/file2.go",
	}
	assert.Equal(t, expected, paths)
}

func TestWalk_IgnoreFilePrecedence(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	setupWalkGitRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "sub")
	subSubDir := filepath.Join(subDir, "subsub")
	createTestDir(t, subSubDir)

	createTestFile(t, filepath.Join(tmpDir, ".sideignore"), ".gitignore\n.ignore\n.sideignore")
	createTestFile(t, filepath.Join(tmpDir, ".gitignore"), "*.txt\n")
	createTestFile(t, filepath.Join(subDir, ".gitignore"), "!important.txt\n")
	createTestFile(t, filepath.Join(subDir, ".ignore"), "*.go\n")
	createTestFile(t, filepath.Join(subSubDir, ".sideignore"), "!special.go\n.sideignore\n")

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
		createTestFile(t, path, "content")
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
