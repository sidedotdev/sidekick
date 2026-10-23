package env

import (
	"context"

	"sidekick/env/localio"
)

type sliceOverlay = localio.SliceOverlay

func walkCodeDirectory(
	ctx context.Context,
	workingDir string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	return (localio.FileSystem{WorkingDirectory: workingDir}).Walk(ctx, ignoreFileNames, handleEntry)
}

func walkCodeDirectoryEntries(
	ctx context.Context,
	workingDir string,
	ignoreFileNames []string,
	handleEntry func(WalkEntryWithContent) error,
) error {
	return (localio.FileSystem{WorkingDirectory: workingDir}).WalkEntries(ctx, ignoreFileNames, handleEntry)
}

func ignoreFilesWithGitignore(names []string) []string {
	return localio.IgnoreFilesWithGitignore(names)
}
