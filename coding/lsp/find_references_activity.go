package lsp

import (
	"context"
	"fmt"
	"sidekick/coding/lsp/core"
	"sidekick/env"
	"sidekick/utils"

	"go.opentelemetry.io/otel"
)

var lspTracer = otel.Tracer("sidekick/coding/lsp")

// NewLSPActivities creates a new LSPActivities instance with proper initialization
func NewLSPActivities(lspClientProvider func(lang string) LSPClient) *LSPActivities {
	return &LSPActivities{Sessions: core.Sessions{
		LSPClientProvider:  lspClientProvider,
		InitializedClients: make(map[string]LSPClient),
	}}
}

// FindReferencesActivityInput represents the input for the FindReferencesActivity function
type FindReferencesActivityInput struct {
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolText       string
	Range            *Range // Optional

	// PreloadedFileContent, when non-nil, is used as RelativeFilePath's
	// content instead of reading it from the environment. It is intentionally
	// not serialized: activity invocations always read from the env.
	PreloadedFileContent []byte `json:"-"`
}

// FindReferencesActivity finds references for the given input using the LSP client.
// Returns ErrUnsupportedLanguage (wrapped) for languages without LSP support.
// Callers that want to silently skip unsupported languages should check with
// IsSupportedLanguage before calling, or use errors.Is on the returned error.
func (lspa *LSPActivities) FindReferencesActivity(ctx context.Context, input FindReferencesActivityInput) ([]Location, error) {
	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	lang := utils.InferLanguageNameFromFilePath(input.RelativeFilePath)
	ctx, lspClient, release, err := lspa.findOrInitClient(ctx, baseDir, lang, &input.EnvContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to find or initialize lsp client: %w", err)
	}
	defer release()

	fileBytes := input.PreloadedFileContent
	if fileBytes == nil {
		fileBytes, err = input.EnvContainer.Env.ReadFile(ctx, input.RelativeFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
	}
	return core.References(ctx, lspClient, baseDir, input.RelativeFilePath, fileBytes, input.SymbolText, input.Range)
}
