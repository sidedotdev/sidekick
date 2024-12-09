package lsp

import (
	"bufio"
	"context"
	"os"
	"path"
	"sidekick/utils"
	"strings"
	"sync"
)

// TODO /gen create an integration test for all methods of LSPActivities, using
// t.TempDir() to create a temporary directory, creating go files and then
// directly calling the activity methods
type LSPActivities struct {
	//StreamAccessor    db.StreamAccessor
	LSPClientProvider     func(language string) LSPClient
	InitializedClients    map[string]LSPClient
	InitializationLockers sync.Map
}

type LSPDefinitionLocationsRequest struct {
	RepoDir  string `json:"repo_dir"`
	FilePath string `json:"file_path"`
	// The list of symbol names in the file to get the definition locations of,
	// eg: "SomeFunction", or "SomeStruct", or "recieiver.SomeMethod", or
	// "some_package.SomeThing", or "someConst" or "aVariableName"
	Symbols []string `json:"symbols"`
}

type SymbolDefinitionLocation struct {
	Symbol   string   `json:"symbol"`
	Location Location `json:"location"`
	Error    string   `json:"error"`
}

// TODO /gen use this function as a fallback when getting symbol definitions
// that are not found via tree-sitter in extractCodeContext
func (la *LSPActivities) GetSymbolDefinitionLocations(ctx context.Context, requests []LSPDefinitionLocationsRequest) ([]SymbolDefinitionLocation, error) {
	var allSymbolDefinitions []SymbolDefinitionLocation
	for _, request := range requests {
		symbolDefinitions, err := la.GetSingleFileDefinitions(ctx, request)
		if err != nil {
			return nil, err
		}
		allSymbolDefinitions = append(allSymbolDefinitions, symbolDefinitions...)
	}
	return allSymbolDefinitions, nil
}

func (la *LSPActivities) GetSingleFileDefinitions(ctx context.Context, request LSPDefinitionLocationsRequest) ([]SymbolDefinitionLocation, error) {
	// Step 1: Find the Position of each symbol in the file.
	positions, err := findSymbolPositions(ctx, request.FilePath, request.Symbols)
	if err != nil {
		return []SymbolDefinitionLocation{}, err
	}

	// Step 2: Initialize the lsp client and invoke its TextDocumentDefinition function to get the definition of each symbol.
	langName := utils.InferLanguageNameFromFilePath(request.FilePath)
	lspClient, err := la.findOrInitClient(ctx, request.RepoDir, langName)
	if err != nil {
		return []SymbolDefinitionLocation{}, err
	}
	fileURI := convertFilePathToURI(request.RepoDir, request.FilePath)
	symbolDefinitions := make([]SymbolDefinitionLocation, 0, len(positions))
	for _, position := range positions {
		locations, err := lspClient.TextDocumentDefinition(ctx, fileURI, position.Line, position.Character)
		if err != nil {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				// FIXME include the symbol name here
				// Symbol: symbolPosition.symbol,
				Error: err.Error(),
			})
			continue
		}

		for _, location := range locations {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				// FIXME include the symbol name here
				// Symbol: symbolPosition.symbol,
				Location: location,
			})
		}
	}

	// Step 3: Use the Location responses and read the location file to get the definition of each symbol based on given range
	return symbolDefinitions, nil
}

// TODO return this instead of Position in findSymbolPositions
type symbolPosition struct {
	symbol   string
	position Position
}

// findSymbolPositions finds the positions of symbols in the given file.
func findSymbolPositions(ctx context.Context, filePath string, symbols []string) ([]Position, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// FIXME this might find multiple positions for the same symbol, we should
	// limit it the first one. less naively, we should use the positions for
	// each unique type of symbol match, but the easiest way to do this might
	// just be to get all the defintions and then remove dups, but this could
	// also be extremely slow with a symbol used many times.
	var positions []Position
	scanner := bufio.NewScanner(file)
	for i := 0; scanner.Scan(); i++ {
		line := scanner.Text()
		for _, symbol := range symbols {
			if strings.Contains(line, symbol) {
				positions = append(positions, Position{Line: i, Character: strings.Index(line, symbol)})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return positions, nil
}

// convertFilePathToURI converts a file path to a URI.
func convertFilePathToURI(repoDir, filePath string) string {
	return "file://" + path.Join(repoDir, filePath)
}
