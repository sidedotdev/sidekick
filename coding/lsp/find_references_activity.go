package lsp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"sync"
)

// NewLSPActivities creates a new LSPActivities instance with proper initialization
func NewLSPActivities(lspClientProvider func(lang string) LSPClient) *LSPActivities {
	return &LSPActivities{
		LSPClientProvider:     lspClientProvider,
		InitializedClients:    make(map[string]LSPClient),
		InitializationLockers: sync.Map{},
	}
}

// FindReferencesActivityInput represents the input for the FindReferencesActivity function
type FindReferencesActivityInput struct {
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolText       string
	Range            *Range // Optional
}

// FindReferencesActivity finds references for the given input using the LSP client
func (lspa *LSPActivities) FindReferencesActivity(ctx context.Context, input FindReferencesActivityInput) ([]Location, error) {
	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	lang := utils.InferLanguageNameFromFilePath(input.RelativeFilePath)
	lspClient, err := lspa.findOrInitClient(ctx, baseDir, lang)
	if err != nil {
		return nil, fmt.Errorf("failed to find or initialize lsp client: %w", err)
	}

	absoluteFilepath := filepath.Join(baseDir, input.RelativeFilePath)
	file, err := os.Open(absoluteFilepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)

	position, err := findSymbolPosition(reader, input.Range, input.SymbolText)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol position: %w", err)
	}

	uri := "file://" + absoluteFilepath
	references, err := lspClient.TextDocumentReferences(ctx, uri, position.Line, position.Character)
	if err != nil {
		return nil, fmt.Errorf("failed to find references: %w", err)
	}

	return references, nil
}

func (lspa *LSPActivities) findOrInitClient(ctx context.Context, baseDir string, lang string) (LSPClient, error) {
	key := baseDir + ":" + lang

	// init lsp client once per baseDir and lang
	locker, _ := lspa.InitializationLockers.LoadOrStore(key, &sync.Mutex{})
	locker.(*sync.Mutex).Lock()
	defer locker.(*sync.Mutex).Unlock()

	if lspa.InitializedClients == nil {
		lspa.InitializedClients = make(map[string]LSPClient)
	}

	lspClient, ok := lspa.InitializedClients[key]
	if !ok {
		// Initialize LSP client
		lspClient = lspa.LSPClientProvider(lang)
		rootUri := "file://" + baseDir
		params := InitializeParams{
			ProcessID:    1,
			RootURI:      rootUri,
			Capabilities: defaultClientCapabilities,
		}
		_, err := lspClient.Initialize(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize LSP client: %w", err)
		}
		lspa.InitializedClients[key] = lspClient
	}

	return lspClient, nil
}

// findSymbolPosition finds the position of the last character of the first matching text in the file content
func findSymbolPosition(reader io.Reader, fileRange *Range, symbolText string) (Position, error) {
	scanner := bufio.NewScanner(reader)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if fileRange != nil && (lineNum < fileRange.Start.Line || lineNum > fileRange.End.Line) {
			lineNum++
			continue
		}
		index := strings.Index(line, symbolText)
		if index != -1 {
			return Position{
				Line:      lineNum,
				Character: index + len(symbolText) - 1,
			}, nil
		}
		lineNum++
	}
	return Position{}, fmt.Errorf("symbol not found in file content")
}
