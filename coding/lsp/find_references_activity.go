package lsp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var lspTracer = otel.Tracer("sidekick/coding/lsp")

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

	uri, err := url.Parse("file://" + absoluteFilepath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse uri file://%s: %w", absoluteFilepath, err)
	}
	references, err := lspClient.TextDocumentReferences(ctx, uri.String(), position.Line, position.Character)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke lsp text document references: %w", err)
	}

	return references, nil
}

func (lspa *LSPActivities) findOrInitClient(ctx context.Context, baseDir string, lang string) (LSPClient, error) {
	_, span := lspTracer.Start(ctx, "findOrInitClient")
	defer span.End()
	span.SetAttributes(attribute.String("language", lang), attribute.String("baseDir", baseDir))

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
		span.SetAttributes(attribute.Bool("initialized", true))
		// Initialize LSP client
		lspClient = lspa.LSPClientProvider(lang)
		rootUri, err := url.Parse("file://" + baseDir)
		if err != nil {
			err = fmt.Errorf("failed to parse rootUri file://%s: %w", baseDir, err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		params := InitializeParams{
			ProcessID:    1,
			RootURI:      rootUri.String(),
			Capabilities: defaultClientCapabilities,
		}
		_, err = lspClient.Initialize(ctx, params)
		if err != nil {
			err = fmt.Errorf("failed to initialize LSP client: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		lspa.InitializedClients[key] = lspClient
	} else {
		span.SetAttributes(attribute.Bool("cached", true))
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
