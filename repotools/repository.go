package repotools

import (
	"context"

	"sidekick/env/localio"
)

// Repository is the file access a tool implementation needs. Paths may be
// relative to the working directory or absolute: language servers point at
// dependency sources outside the repository.
type Repository interface {
	GetWorkingDirectory() string
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// Walk visits every non-ignored entry with its absolute path.
	Walk(ctx context.Context, handleEntry func(path string, isDir bool) error) error
	// WalkEntries is Walk with access to file content as observed by the
	// walker, avoiding a separate read per file.
	WalkEntries(ctx context.Context, handleEntry func(localio.WalkEntryWithContent) error) error
}

// IgnoreFileNames are the ignore files honored when walking a repository.
var IgnoreFileNames = []string{".gitignore", ".ignore", ".sideignore"}

// LocalRepository serves a repository from the local filesystem.
type LocalRepository struct {
	localio.FileSystem
	// IgnoreFileNames lists the ignore files honored while walking.
	IgnoreFileNames []string
}

func (r LocalRepository) GetWorkingDirectory() string {
	return r.WorkingDirectory
}

func (r LocalRepository) Walk(ctx context.Context, handleEntry func(path string, isDir bool) error) error {
	return r.FileSystem.Walk(ctx, r.IgnoreFileNames, handleEntry)
}

func (r LocalRepository) WalkEntries(ctx context.Context, handleEntry func(localio.WalkEntryWithContent) error) error {
	return r.FileSystem.WalkEntries(ctx, r.IgnoreFileNames, handleEntry)
}
