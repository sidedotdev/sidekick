package coding

import (
	"context"
	"fmt"

	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/env/localio"
	"sidekick/repotools"
	"sidekick/sideagent"
)

type CodingActivities struct {
	LSPActivities        *lsp.LSPActivities
	TreeSitterActivities *tree_sitter.TreeSitterActivities
}

type FileSymDefRequest = repotools.FileSymDefRequest
type RequestedSymbol = repotools.RequestedSymbol
type SymDefResults = repotools.SymDefResults

const DefaultNumContextLines = repotools.DefaultNumContextLines

func CodeFenceStartForLanguage(langName string) string {
	return repotools.CodeFenceStartForLanguage(langName)
}

type DirectorySymDefRequest struct {
	EnvContainer          env.EnvContainer
	Requests              []FileSymDefRequest
	NumContextLines       *int
	IncludeRelatedSymbols bool
}

func (r DirectorySymDefRequest) toolRequest() repotools.DirectorySymDefRequest {
	return repotools.DirectorySymDefRequest{
		Requests:              r.Requests,
		NumContextLines:       r.NumContextLines,
		IncludeRelatedSymbols: r.IncludeRelatedSymbols,
	}
}

// BulkGetSymbolDefinitions outputs symbol definitions formatted per file;
// symbols that were not found are included in the failures. Environments with
// a sidekick agent run the whole tool inside the sandbox, so file reads and
// language-server traffic never cross the network.
func (ca *CodingActivities) BulkGetSymbolDefinitions(ctx context.Context, dirSymDefRequest DirectorySymDefRequest) (SymDefResults, error) {
	if provider, ok := dirSymDefRequest.EnvContainer.Env.(env.AgentProvider); ok {
		var results SymDefResults
		err := provider.WithAgent(ctx, func(client *sideagent.Client) error {
			var err error
			results, err = client.BulkGetSymbolDefinitions(ctx, dirSymDefRequest.EnvContainer.Env.GetWorkingDirectory(), dirSymDefRequest.toolRequest())
			return err
		})
		return results, err
	}

	tool, err := ca.symbolDefinitions(dirSymDefRequest.EnvContainer)
	if err != nil {
		return SymDefResults{}, err
	}
	return tool.BulkGetSymbolDefinitions(ctx, dirSymDefRequest.toolRequest())
}

type RelatedSymbol = repotools.RelatedSymbol

type RelatedSymbolsActivityInput struct {
	SymbolText       string
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolRange      *lsp.Range

	// PreloadedFileContent, when non-nil, is used as RelativeFilePath's
	// content instead of reading it from the environment.
	PreloadedFileContent []byte `json:"-"`
}

// RelatedSymbolsActivity finds the symbols whose definitions reference the
// requested symbol.
func (ca *CodingActivities) RelatedSymbolsActivity(ctx context.Context, input RelatedSymbolsActivityInput) ([]RelatedSymbol, error) {
	tool, err := ca.symbolDefinitions(input.EnvContainer)
	if err != nil {
		return nil, err
	}
	return tool.RelatedSymbols(ctx, tool.Repository, repotools.RelatedSymbolsRequest{
		SymbolText:           input.SymbolText,
		RelativeFilePath:     input.RelativeFilePath,
		SymbolRange:          input.SymbolRange,
		PreloadedFileContent: input.PreloadedFileContent,
	})
}

func (ca *CodingActivities) symbolDefinitions(container env.EnvContainer) (repotools.SymbolDefinitions, error) {
	tool := repotools.SymbolDefinitions{Repository: envRepository{container}}
	if ca.LSPActivities != nil {
		scope, err := ca.LSPActivities.ClientScope(container)
		if err != nil {
			return tool, fmt.Errorf("identify analysis environment: %w", err)
		}
		tool.Sessions = &ca.LSPActivities.Sessions
		tool.Scope = scope
	}
	return tool, nil
}

// envRepository adapts an environment to repotools.Repository.
type envRepository struct {
	container env.EnvContainer
}

func (r envRepository) GetWorkingDirectory() string {
	return r.container.Env.GetWorkingDirectory()
}

func (r envRepository) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return r.container.Env.ReadFile(ctx, path)
}

func (r envRepository) Walk(ctx context.Context, handleEntry func(path string, isDir bool) error) error {
	return r.container.Env.Walk(ctx, common.SidekickIgnoreFileNames, handleEntry)
}

func (r envRepository) WalkEntries(ctx context.Context, handleEntry func(localio.WalkEntryWithContent) error) error {
	return env.WalkCodeDirectoryEntriesViaEnv(ctx, r.container, handleEntry)
}
