package repotools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"sidekick/coding/tree_sitter/parser"
	"sidekick/env/localio"
	"sidekick/utils"
)

type SearchFiles interface {
	ReadFile(context.Context, string) ([]byte, error)
	MkdirAll(context.Context, string, fs.FileMode) error
	WriteFile(context.Context, string, []byte, fs.FileMode) error
}

type RepositorySearchRunner struct {
	Context   context.Context
	Directory string
	Files     SearchFiles
	Command   func(string, ...string) (localio.RunCommandOutput, error)
}

func (r RepositorySearchRunner) RunCommand(command string, args ...string) (localio.RunCommandOutput, error) {
	if err := r.Context.Err(); err != nil {
		return localio.RunCommandOutput{}, err
	}
	return r.Command(command, args...)
}

func (r RepositorySearchRunner) EnsureCoreIgnoreFile() (string, error) {
	if err := r.Files.MkdirAll(r.Context, filepath.Dir(CoreIgnoreRelPath), 0755); err != nil {
		return "", err
	}
	if err := r.Files.WriteFile(r.Context, CoreIgnoreRelPath, []byte(CoreIgnoreFileContent), 0644); err != nil {
		return "", err
	}
	return filepath.Join(r.Directory, CoreIgnoreRelPath), nil
}

func (r RepositorySearchRunner) FileSymbols(path string) ([]parser.Symbol, error) {
	content, err := r.Files.ReadFile(r.Context, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return parser.GetFileSymbolsFromBytes(path, utils.InferLanguageNameFromFilePath(path), content)
}

func (r RepositorySearchRunner) Legacy(string) bool {
	return false
}
