package localio

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

// WorkingDirectory is not a confinement boundary: dependency definitions may
// live outside the repository.
type FileSystem struct {
	WorkingDirectory string
}

func (f FileSystem) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.WorkingDirectory, path)
}

func (f FileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(f.resolve(path))
}

func (f FileSystem) ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadDir(f.resolve(path))
}

func (f FileSystem) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(f.resolve(path), data, perm)
}

func (f FileSystem) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(f.resolve(path), perm)
}

func (f FileSystem) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Stat(f.resolve(path))
}

func (f FileSystem) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(f.resolve(path))
}

func (f FileSystem) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if dir != "" {
		dir = f.resolve(dir)
	}
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return name, err
	}
	return name, nil
}
