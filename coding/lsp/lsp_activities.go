package lsp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sidekick/env"
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

	var lineRange *Range
	if request.ReferenceLine != "" {
		lineRange = findReferenceLineRange(fileBytes, request.ReferenceLine)
		if lineRange == nil {
			symbolDefinitions := make([]SymbolDefinitionLocation, 0, len(request.Symbols))
			for _, symbol := range request.Symbols {
				symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
					Symbol: symbol,
					Error:  buildReferenceLineMismatchError(fileBytes, request.FilePath, request.ReferenceLine, symbol),
				})
			}
			return symbolDefinitions, nil
		}
	}

	langName := utils.InferLanguageNameFromFilePath(request.FilePath)
	repoDir := request.EnvContainer.Env.GetWorkingDirectory()
	lspClient, err := la.findOrInitClient(ctx, repoDir, langName, request.EnvContainer)
	if err != nil {
		return []SymbolDefinitionLocation{}, err
	}
	fileURI := convertFilePathToURI(repoDir, request.FilePath)

	symbolDefinitions := make([]SymbolDefinitionLocation, 0, len(request.Symbols))
	for _, symbol := range request.Symbols {
		positions := findAllSymbolPositions(fileBytes, lineRange, symbol)
		if len(positions) == 0 {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				Symbol: symbol,
				Error:  "symbol not found in file content",
			})
			continue
		}

		seen := make(map[Location]struct{})
		var lastErr error
		foundAny := false
		for _, pos := range positions {
			locations, defErr := lspClient.TextDocumentDefinition(ctx, fileURI, pos.Line, pos.Character)
			if defErr != nil {
				lastErr = defErr
				continue
			}
			for _, location := range locations {
				if _, ok := seen[location]; ok {
					continue
				}
				seen[location] = struct{}{}
				symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
					Symbol:   symbol,
					Location: location,
				})
				foundAny = true
			}
		}
		if !foundAny && lastErr != nil {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				Symbol: symbol,
				Error:  lastErr.Error(),
			})
		}
	}

	return symbolDefinitions, nil
}

// findAllSymbolPositions returns the position of every occurrence of
// symbolText within data, pointing at the last character of each match (matching
// findSymbolPosition's convention). When fileRange is non-nil, only matches on
// lines within that range are returned. Multiple matches on the same line are
// each reported.
func findAllSymbolPositions(data []byte, fileRange *Range, symbolText string) []Position {
	if symbolText == "" {
		return nil
	}
	var positions []Position
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		if fileRange != nil && (i < fileRange.Start.Line || i > fileRange.End.Line) {
			continue
		}
		line := scanner.Text()
		searchFrom := 0
		for searchFrom <= len(line) {
			idx := strings.Index(line[searchFrom:], symbolText)
			if idx < 0 {
				break
			}
			absoluteIdx := searchFrom + idx
			positions = append(positions, Position{
				Line:      i,
				Character: absoluteIdx + len(symbolText) - 1,
			})
			searchFrom = absoluteIdx + len(symbolText)
		}
	}
	return positions
}

// buildReferenceLineMismatchError formats an error message explaining that
// the caller-provided reference_line could not be located in the file, and
// lists the actual lines (1-based numbers + literal content) where the symbol
// appears so the caller can retry with a correct reference line.
func buildReferenceLineMismatchError(data []byte, filePath, referenceLine, symbol string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reference_line %q did not match any line in %s", referenceLine, filePath)
	occurrences := findSymbolOccurrenceLines(data, symbol)
	if len(occurrences) == 0 {
		fmt.Fprintf(&b, "; symbol %q does not appear in the file", symbol)
		return b.String()
	}
	fmt.Fprintf(&b, "; symbol %q occurs on the following lines:", symbol)
	for _, occ := range occurrences {
		fmt.Fprintf(&b, "\n  - line %d: %s", occ.lineNumber, occ.content)
	}
	return b.String()
}

type symbolOccurrenceLine struct {
	lineNumber int
	content    string
}

// findSymbolOccurrenceLines returns each line in data that contains symbolText,
// reported as a 1-based line number and the literal line content. A line that
// contains multiple occurrences is reported once.
func findSymbolOccurrenceLines(data []byte, symbolText string) []symbolOccurrenceLine {
	if symbolText == "" {
		return nil
	}
	var occurrences []symbolOccurrenceLine
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		line := scanner.Text()
		if strings.Contains(line, symbolText) {
			occurrences = append(occurrences, symbolOccurrenceLine{
				lineNumber: i + 1,
				content:    line,
			})
		}
	}
	return occurrences
}

// findReferenceLineRange returns a single-line Range covering the first line in
// data that contains the given snippet (after trimming surrounding whitespace),
// or nil if no such line is found. Callers use a nil return to signal that the
// caller-provided reference_line could not be located.
func findReferenceLineRange(data []byte, snippet string) *Range {
	trimmed := strings.TrimSpace(snippet)
	if trimmed == "" {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		line := scanner.Text()
		if strings.Contains(line, trimmed) {
			return &Range{
				Start: Position{Line: i, Character: 0},
				End:   Position{Line: i, Character: len(line)},
			}
		}
	}
	return nil
}

// TODO return this instead of Position in findSymbolPositions
type symbolPosition struct {
	symbol   string
	position Position
}

// findSymbolPositionsFromBytes finds symbol positions in pre-read file content.
func findSymbolPositionsFromBytes(data []byte, symbols []string) ([]Position, error) {
	return findSymbolPositionsFromReader(bytes.NewReader(data), symbols)
}

func findSymbolPositionsFromReader(r io.Reader, symbols []string) ([]Position, error) {
	// FIXME this might find multiple positions for the same symbol, we should
	// limit it the first one. less naively, we should use the positions for
	// each unique type of symbol match, but the easiest way to do this might
	// just be to get all the defintions and then remove dups, but this could
	// also be extremely slow with a symbol used many times.
	var positions []Position
	scanner := bufio.NewScanner(r)
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
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, nil)
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
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, nil)
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
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, nil)
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
	lspClient, err := lspa.findOrInitClient(ctx, input.RepoDir, langName, nil)
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
