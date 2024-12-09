package lsp

import "context"

// MockLSPClient is a mock implementation of the LSPClient interface for testing.
type MockLSPClient struct {
	InitializeFunc                 func(ctx context.Context, params InitializeParams) (InitializeResponse, error)
	TextDocumentDefinitionFunc     func(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentCodeActionFunc     func(ctx context.Context, params CodeActionParams) ([]CodeAction, error)
	TextDocumentImplementationFunc func(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentReferencesFunc     func(ctx context.Context, uri string, line int, character int) ([]Location, error)
}

func (m MockLSPClient) Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
	// NOTE: we only want to return a default response for Initialize, not the
	// other methods, since those should be overriden if called.
	if m.InitializeFunc == nil {
		return InitializeResponse{}, nil
	}
	return m.InitializeFunc(ctx, params)
}

func (m MockLSPClient) TextDocumentDefinition(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if m.TextDocumentDefinitionFunc == nil {
		panic("TextDocumentDefinitionFunc is not set on mock lsp client")
	}
	return m.TextDocumentDefinitionFunc(ctx, uri, line, character)
}

func (m MockLSPClient) TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
	if m.TextDocumentCodeActionFunc == nil {
		panic("TextDocumentCodeActionFunc is not set on mock lsp client")
	}
	return m.TextDocumentCodeActionFunc(ctx, params)
}

func (m MockLSPClient) TextDocumentImplementation(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if m.TextDocumentImplementationFunc == nil {
		panic("TextDocumentImplementationFunc is not set on mock lsp client")
	}
	return m.TextDocumentImplementationFunc(ctx, uri, line, character)
}

func (m MockLSPClient) TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if m.TextDocumentReferencesFunc == nil {
		panic("TextDocumentReferencesFunc is not set on mock lsp client")
	}
	return m.TextDocumentReferencesFunc(ctx, uri, line, character)
}

func (m MockLSPClient) GetServerCapabilities() ServerCapabilities {
	return ServerCapabilities{}
}
