package localio

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileSystemWalk(t *testing.T) {
	t.Parallel()

	for _, useGit := range []bool{false, true} {
		name := "plain directory"
		if useGit {
			name = "git checkout"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			ctx := context.Background()
			fixtures := map[string]string{
				".gitignore":         "generated/\n",
				".sideignore":        "secret.txt\n",
				"kept.go":            "package before\n",
				"deleted.go":         "package deleted\n",
				"secret.txt":         "secret",
				"generated/file.go":  "package generated\n",
				"nested/.sideignore": "skip.go\n",
				"nested/file.go":     "package nested\n",
				"nested/skip.go":     "package skipped\n",
			}
			for path, content := range fixtures {
				abs := filepath.Join(root, filepath.FromSlash(path))
				require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0755))
				require.NoError(t, os.WriteFile(abs, []byte(content), 0644))
			}
			if useGit {
				for _, args := range [][]string{
					{"init", "-q"},
					{"add", "."},
					{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"},
				} {
					cmd := exec.CommandContext(ctx, "git", args...)
					cmd.Dir = root
					output, err := cmd.CombinedOutput()
					require.NoError(t, err, "%s", output)
				}
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), []byte("package after\n"), 0644))
			require.NoError(t, os.Remove(filepath.Join(root, "deleted.go")))
			require.NoError(t, os.WriteFile(filepath.Join(root, "added.go"), []byte("package added\n"), 0644))

			files := FileSystem{WorkingDirectory: root}
			ignoreNames := []string{".gitignore", ".sideignore"}
			actual := make(map[string]string)
			err := files.WalkEntries(ctx, ignoreNames, func(entry WalkEntryWithContent) error {
				if entry.IsDir {
					return nil
				}
				reader, err := entry.Open(ctx)
				if err != nil {
					return err
				}
				data, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if readErr != nil {
					return readErr
				}
				if closeErr != nil {
					return closeErr
				}
				relative, err := filepath.Rel(root, entry.Path)
				if err != nil {
					return err
				}
				actual[filepath.ToSlash(relative)] = string(data)
				return nil
			})
			require.NoError(t, err)
			expected := map[string]string{
				".gitignore":         "generated/\n",
				".sideignore":        "secret.txt\n",
				"kept.go":            "package after\n",
				"added.go":           "package added\n",
				"nested/.sideignore": "skip.go\n",
				"nested/file.go":     "package nested\n",
			}
			require.Equal(t, expected, actual)

			var paths []string
			require.NoError(t, files.Walk(ctx, ignoreNames, func(path string, isDir bool) error {
				if !isDir {
					relative, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					paths = append(paths, filepath.ToSlash(relative))
				}
				return nil
			}))
			var expectedPaths []string
			for path := range expected {
				expectedPaths = append(expectedPaths, path)
			}
			require.ElementsMatch(t, expectedPaths, paths)
		})
	}
}

func TestFileSystemWalkCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"first.go", "second.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("package example\n"), 0644))
	}
	files := FileSystem{WorkingDirectory: root}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visits := 0
	err := files.Walk(ctx, nil, func(path string, isDir bool) error {
		visits++
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, visits)
}
