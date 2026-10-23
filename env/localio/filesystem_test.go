package localio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileSystemPaths(t *testing.T) {
	t.Parallel()

	for _, pathMode := range []string{"relative", "absolute", "parent"} {
		t.Run(pathMode, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			root := filepath.Join(base, "repo")
			require.NoError(t, os.MkdirAll(root, 0755))
			files := FileSystem{WorkingDirectory: root}
			ctx := context.Background()

			filePath := filepath.Join("nested", "source.go")
			expectedPath := filepath.Join(root, filePath)
			switch pathMode {
			case "absolute":
				filePath = filepath.Join(base, "dependency", "source.go")
				expectedPath = filePath
			case "parent":
				filePath = filepath.Join("..", "dependency", "source.go")
				expectedPath = filepath.Join(base, "dependency", "source.go")
			}

			dir := filepath.Dir(filePath)
			require.NoError(t, files.MkdirAll(ctx, dir, 0755))
			content := []byte("package example\n")
			require.NoError(t, files.WriteFile(ctx, filePath, content, 0644))

			data, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			require.Equal(t, content, data)

			data, err = files.ReadFile(ctx, filePath)
			require.NoError(t, err)
			require.Equal(t, content, data)

			info, err := files.Stat(ctx, filePath)
			require.NoError(t, err)
			require.True(t, info.Mode().IsRegular())
			require.Equal(t, int64(len(content)), info.Size())

			entries, err := files.ReadDir(ctx, dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "source.go", entries[0].Name())

			tempPath, err := files.CreateTemp(ctx, dir, "source-*")
			require.NoError(t, err)
			require.Equal(t, filepath.Dir(expectedPath), filepath.Dir(tempPath))
			data, err = files.ReadFile(ctx, tempPath)
			require.NoError(t, err)
			require.Empty(t, data)
			require.NoError(t, files.Remove(ctx, tempPath))

			require.NoError(t, files.Remove(ctx, filePath))
			_, err = os.Stat(expectedPath)
			require.ErrorIs(t, err, os.ErrNotExist)
			_, err = files.ReadFile(ctx, filePath)
			require.ErrorIs(t, err, os.ErrNotExist)
			require.NoError(t, files.Remove(ctx, dir))
		})
	}
}

func TestFileSystemCanceledOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(context.Context, FileSystem) error
	}{
		{
			name: "read file",
			run: func(ctx context.Context, files FileSystem) error {
				_, err := files.ReadFile(ctx, "file.txt")
				return err
			},
		},
		{
			name: "read directory",
			run: func(ctx context.Context, files FileSystem) error {
				_, err := files.ReadDir(ctx, ".")
				return err
			},
		},
		{
			name: "write file",
			run: func(ctx context.Context, files FileSystem) error {
				return files.WriteFile(ctx, "file.txt", []byte("changed"), 0644)
			},
		},
		{
			name: "create directory",
			run: func(ctx context.Context, files FileSystem) error {
				return files.MkdirAll(ctx, "extra", 0755)
			},
		},
		{
			name: "stat",
			run: func(ctx context.Context, files FileSystem) error {
				_, err := files.Stat(ctx, "file.txt")
				return err
			},
		},
		{
			name: "remove",
			run: func(ctx context.Context, files FileSystem) error {
				return files.Remove(ctx, "file.txt")
			},
		},
		{
			name: "create temporary file",
			run: func(ctx context.Context, files FileSystem) error {
				_, err := files.CreateTemp(ctx, ".", "source-*")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			content := []byte("original")
			require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), content, 0644))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			require.ErrorIs(t, tt.run(ctx, FileSystem{WorkingDirectory: root}), context.Canceled)
			data, err := os.ReadFile(filepath.Join(root, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, content, data)
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}
