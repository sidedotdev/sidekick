package core

import "context"

type LSPClient interface {
	Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error)
	TextDocumentDefinition(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error)
	TextDocumentImplementation(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error)
	PrepareCallHierarchy(ctx context.Context, uri string, line int, character int) ([]CallHierarchyItem, error)
	CallHierarchyIncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error)
	GetServerCapabilities() ServerCapabilities

	// Text document synchronization notifications
	TextDocumentDidOpen(ctx context.Context, params DidOpenTextDocumentParams) error
	TextDocumentDidChange(ctx context.Context, params DidChangeTextDocumentParams) error
	TextDocumentDidSave(ctx context.Context, params DidSaveTextDocumentParams) error
	TextDocumentDidClose(ctx context.Context, params DidCloseTextDocumentParams) error
}