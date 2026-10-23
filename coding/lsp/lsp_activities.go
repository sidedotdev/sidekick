package lsp

import (
	"context"
	"fmt"
	"sidekick/coding/lsp/core"
	"sidekick/env"
	"sidekick/utils"
)

// TODO /gen create an integration test for all methods of LSPActivities, using
// t.TempDir() to create a temporary directory, creating go files and then
// directly calling the activity methods
type LSPActivities struct {
	core.Sessions
}

type LSPDefinitionLocationsRequest struct {
	FilePath     string            `json:"file_path"`
	EnvContainer *env.EnvContainer `json:"envContainer,omitempty"`
	// The list of symbol names in the file to get the definition locations of,
	// eg: "SomeFunction", or "SomeStruct", or "recieiver.SomeMethod", or
	// "some_package.SomeThing", or "someConst" or "aVariableName"
	Symbols []string `json:"symbols"`
	// ReferenceLine, when set, is a literal source line (or distinctive
	// substring of one) from FilePath used to constrain the symbol-position
	// search to a specific line before invoking go-to-definition. When the
	// snippet cannot be located in the file, an error is returned for each
	// symbol listing the actual lines (with 1-based line numbers) where that
	// symbol occurs, so the caller can retry with a correct reference line.
	// When unset, every occurrence of each symbol is resolved and the
	// resulting definition locations are de-duplicated.
	ReferenceLine string `json:"reference_line,omitempty"`

	// PreloadedFileContent, when non-nil, is used as FilePath's content
	// instead of reading it from the environment. It is intentionally not
	// serialized: activity invocations always read from the env.
	PreloadedFileContent []byte `json:"-"`
}

type SymbolDefinitionLocation = core.SymbolDefinitionLocation

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
	if request.EnvContainer == nil {
		return nil, fmt.Errorf("EnvContainer is required in LSPDefinitionLocationsRequest")
	}
	fileBytes := request.PreloadedFileContent
	if fileBytes == nil {
		var readErr error
		fileBytes, readErr = request.EnvContainer.Env.ReadFile(ctx, request.FilePath)
		if readErr != nil {
			return []SymbolDefinitionLocation{}, readErr
		}
	}
	langName := utils.InferLanguageNameFromFilePath(request.FilePath)
	repoDir := request.EnvContainer.Env.GetWorkingDirectory()
	ctx, lspClient, release, err := la.findOrInitClient(ctx, repoDir, langName, request.EnvContainer)
	if err != nil {
		return []SymbolDefinitionLocation{}, err
	}
	defer release()
	return core.DefinitionLocations(ctx, lspClient, repoDir, request.FilePath, fileBytes, request.Symbols, request.ReferenceLine), nil
}

var findSymbolOccurrencePositions = core.FindSymbolOccurrencePositions

// TextDocumentDidOpenActivityInput represents input for the TextDocumentDidOpen notification.
type TextDocumentDidOpenActivityInput struct {
	RepoDir      string            `json:"repo_dir"`
	FilePath     string            `json:"file_path"`
	LanguageID   string            `json:"language_id"`
	Version      int               `json:"version"`
	Text         string            `json:"text"`
	EnvContainer *env.EnvContainer `json:"envContainer,omitempty"`
}

// TextDocumentDidOpenActivity sends a textDocument/didOpen notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidOpenActivity(ctx context.Context, input TextDocumentDidOpenActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	ctx, lspClient, release, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, input.EnvContainer)
	if err != nil {
		return err
	}
	defer release()
	return didOpen(ctx, lspClient, input)
}

func didOpen(ctx context.Context, lspClient LSPClient, input TextDocumentDidOpenActivityInput) error {
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

	fileURI := core.ConvertFilePathToURI(input.RepoDir, input.FilePath)
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
	RepoDir      string            `json:"repo_dir"`
	FilePath     string            `json:"file_path"`
	EnvContainer *env.EnvContainer `json:"envContainer,omitempty"`
}

// TextDocumentDidCloseActivity sends a textDocument/didClose notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidCloseActivity(ctx context.Context, input TextDocumentDidCloseActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	ctx, lspClient, release, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, input.EnvContainer)
	if err != nil {
		return err
	}
	defer release()
	return didClose(ctx, lspClient, input)
}

func didClose(ctx context.Context, lspClient LSPClient, input TextDocumentDidCloseActivityInput) error {
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

	fileURI := core.ConvertFilePathToURI(input.RepoDir, input.FilePath)
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
	EnvContainer   *env.EnvContainer                `json:"envContainer,omitempty"`
}

// TextDocumentDidChangeActivity sends a textDocument/didChange notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidChangeActivity(ctx context.Context, input TextDocumentDidChangeActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	ctx, lspClient, release, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, input.EnvContainer)
	if err != nil {
		return err
	}
	defer release()
	return didChange(ctx, lspClient, input)
}

func didChange(ctx context.Context, lspClient LSPClient, input TextDocumentDidChangeActivityInput) error {
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

	fileURI := core.ConvertFilePathToURI(input.RepoDir, input.FilePath)
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
	RepoDir      string            `json:"repo_dir"`
	FilePath     string            `json:"file_path"`
	Text         *string           `json:"text,omitempty"`
	EnvContainer *env.EnvContainer `json:"envContainer,omitempty"`
}

// TextDocumentDidSaveActivity sends a textDocument/didSave notification to the LSP server.
func (lspa *LSPActivities) TextDocumentDidSaveActivity(ctx context.Context, input TextDocumentDidSaveActivityInput) error {
	langName := utils.InferLanguageNameFromFilePath(input.FilePath)
	ctx, lspClient, release, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, input.EnvContainer)
	if err != nil {
		return err
	}
	defer release()
	return didSave(ctx, lspClient, input)
}

func didSave(ctx context.Context, lspClient LSPClient, input TextDocumentDidSaveActivityInput) error {
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

	fileURI := core.ConvertFilePathToURI(input.RepoDir, input.FilePath)
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
