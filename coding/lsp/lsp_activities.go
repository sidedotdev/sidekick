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

// TextDocumentDidOpenActivityInput represents input for the TextDocumentDidOpen notification.
type TextDocumentDidOpenActivityInput struct {
	RepoDir    string `json:"repo_dir"`
	FilePath   string `json:"file_path"`
	LanguageID string `json:"language_id"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// TextDocumentDidOpenActivity sends a textDocument/didOpen notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidOpenActivity(ctx context.Context, input TextDocumentDidOpenActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName)
	if err != nil {
		return err
	}

	// Check if server supports open/close notifications
	capabilities := lspClient.GetServerCapabilities()
	if syncOptions, ok := capabilities.TextDocumentSync.(map[string]interface{}); ok {
		if openClose, exists := syncOptions["openClose"]; exists {
			if openCloseBool, ok := openClose.(bool); ok && !openCloseBool {
				return nil // Server doesn't support open/close notifications
			}
		} else {
			return nil // Server doesn't support open/close notifications
		}
	}

	fileURI := convertFilePathToURI(input.RepoDir, input.FilePath)
	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        fileURI,
			LanguageID: input.LanguageID,
			Version:    input.Version,
			Text:       input.Text,
		},
	}

	return lspClient.TextDocumentDidOpen(ctx, params)
}

// TextDocumentDidCloseActivityInput represents input for the TextDocumentDidClose notification.
type TextDocumentDidCloseActivityInput struct {
	RepoDir  string `json:"repo_dir"`
	FilePath string `json:"file_path"`
}

// TextDocumentDidCloseActivity sends a textDocument/didClose notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidCloseActivity(ctx context.Context, input TextDocumentDidCloseActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName)
	if err != nil {
		return err
	}

	// Check if server supports open/close notifications
	capabilities := lspClient.GetServerCapabilities()
	if syncOptions, ok := capabilities.TextDocumentSync.(map[string]interface{}); ok {
		if openClose, exists := syncOptions["openClose"]; exists {
			if openCloseBool, ok := openClose.(bool); ok && !openCloseBool {
				return nil // Server doesn't support open/close notifications
			}
		} else {
			return nil // Server doesn't support open/close notifications
		}
	}

	fileURI := convertFilePathToURI(input.RepoDir, input.FilePath)
	params := DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{
			URI: fileURI,
		},
	}

	return lspClient.TextDocumentDidClose(ctx, params)
}

// TextDocumentDidChangeActivityInput represents input for the TextDocumentDidChange notification.
type TextDocumentDidChangeActivityInput struct {
	RepoDir        string                           `json:"repo_dir"`
	FilePath       string                           `json:"file_path"`
	Version        int                              `json:"version"`
	ContentChanges []TextDocumentContentChangeEvent `json:"content_changes"`
}

// TextDocumentDidChangeActivity sends a textDocument/didChange notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidChangeActivity(ctx context.Context, input TextDocumentDidChangeActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName)
	if err != nil {
		return err
	}

	// Check if server supports change notifications
	capabilities := lspClient.GetServerCapabilities()
	if syncOptions, ok := capabilities.TextDocumentSync.(map[string]interface{}); ok {
		if change, exists := syncOptions["change"]; exists {
			if changeNum, ok := change.(float64); ok && changeNum == float64(TextDocumentSyncKindNone) {
				return nil // Server doesn't support change notifications (TextDocumentSyncKind.None)
			}
		} else {
			return nil // chnage not found in capabilities
		}
	}

	fileURI := convertFilePathToURI(input.RepoDir, input.FilePath)
	params := DidChangeTextDocumentParams{
		TextDocument: VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: TextDocumentIdentifier{
				URI: fileURI,
			},
			Version: input.Version,
		},
		ContentChanges: input.ContentChanges,
	}

	return lspClient.TextDocumentDidChange(ctx, params)
}

// TextDocumentDidSaveActivityInput represents input for the TextDocumentDidSave notification.
type TextDocumentDidSaveActivityInput struct {
	RepoDir  string  `json:"repo_dir"`
	FilePath string  `json:"file_path"`
	Text     *string `json:"text,omitempty"`
}

// TextDocumentDidSaveActivity sends a textDocument/didSave notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidSaveActivity(ctx context.Context, input TextDocumentDidSaveActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName)
	if err != nil {
		return err
	}

	// Check if server supports save notifications
	capabilities := lspClient.GetServerCapabilities()
	includeText := true
	if syncOptions, ok := capabilities.TextDocumentSync.(map[string]interface{}); ok {
		if save, exists := syncOptions["save"]; exists {
			if saveBool, ok := save.(bool); ok && !saveBool {
				return nil // Server doesn't support save notifications
			} else if saveMap, ok := save.(map[string]interface{}); ok {
				if includeTextInterface, exists := saveMap["includeText"]; exists {
					if includeTextBool, ok := includeTextInterface.(bool); ok {
						includeText = includeTextBool
					}
				}
			}
		} else {
			return nil // Server doesn't support save notifications
		}
	}

	fileURI := convertFilePathToURI(input.RepoDir, input.FilePath)
	params := DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{
			URI: fileURI,
		},
	}
	if includeText {
		params.Text = input.Text
	}

	return lspClient.TextDocumentDidSave(ctx, params)
}
