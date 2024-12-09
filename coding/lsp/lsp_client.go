package lsp

import (
	"context" // Adding the missing json package for handling JSON data
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/sourcegraph/jsonrpc2"
)

type LSPClient interface {
	Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error)
	TextDocumentDefinition(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error)
	TextDocumentImplementation(ctx context.Context, uri string, line int, character int) ([]Location, error)
	TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error)
	GetServerCapabilities() ServerCapabilities
}

type Jsonrpc2LSPClient struct {
	Conn               *jsonrpc2.Conn
	ServerCapabilities ServerCapabilities
	LanguageName       string
}

type ReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

func (rwc *ReadWriteCloser) Close() error {
	if err := rwc.WriteCloser.Close(); err != nil {
		return err
	}
	if closer, ok := rwc.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

var ErrUnsupportedLanguage = errors.New("unsupported language")

func lspServerStdioReadWriteCloser(languageName string) (*ReadWriteCloser, error) {
	var cmd *exec.Cmd
	switch languageName {
	case "golang":
		cmd = exec.Command("gopls", "-remote=auto", "-logfile=auto", "-debug=:0", "-remote.debug=:0", "-rpc.trace", "-remote.listen.timeout=0")
	default:
		return nil, fmt.Errorf("%v: %s", ErrUnsupportedLanguage, languageName)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cmd.StdinPipe() failed: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cmd.StdoutPipe() failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cmd.Start() failed: %v", err)
	}
	return &ReadWriteCloser{stdout, stdin}, nil
}

type noopHandler struct{}

func (noopHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	//fmt.Printf("\nnoopHandler.Handle called with req: %v\n", req)
}

// TODO make the ReadWriteCloser a parameter, so that multiple different language servers can be supported
func (l *Jsonrpc2LSPClient) Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
	// start lsp server (if needed) and connect to it
	rwc, err := lspServerStdioReadWriteCloser(l.LanguageName)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("gopls failure: %v", err)
	}

	// Setup JSON-RPC 2.0 connection
	(*l).Conn = jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(rwc, jsonrpc2.VSCodeObjectCodec{}), &noopHandler{})

	// Send request and handle response
	var resp InitializeResponse
	err = l.Conn.Call(ctx, "initialize", &params, &resp)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("initialize call failed: %v", err)
	}
	//utils.PrettyPrint(resp) // debug

	l.ServerCapabilities = resp.Capabilities

	// let the LSP server know that we initialized successfully
	err = l.Conn.Call(ctx, "initialized", &map[string]string{}, &map[string]string{})
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("initialized response failed: %v", err)
	}

	return resp, nil
}

// textDocument/definition
func (l *Jsonrpc2LSPClient) TextDocumentDefinition(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentDefinition called before Initialize")
	}
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{
			DocumentURI: uri,
		},
		Position: Position{
			Line:      line,
			Character: character,
		},
	}
	var locations []Location
	err := l.Conn.Call(ctx, "textDocument/definition", params, &locations)
	if err != nil {
		return []Location{}, err
	}
	return locations, nil
}

// textDocument/references
func (l *Jsonrpc2LSPClient) TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentReferences called before Initialize")
	}
	params := ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{
				DocumentURI: uri,
			},
			Position: Position{
				Line:      line,
				Character: character,
			},
		},
		Context: ReferenceContext{
			IncludeDeclaration: false, // TODO /gen take in as an argument instead of hard-coding
		},
	}
	var locations []Location
	err := l.Conn.Call(ctx, "textDocument/references", params, &locations)
	if err != nil {
		return []Location{}, err
	}
	return locations, nil
}

// textDocument/codeAction
func (l *Jsonrpc2LSPClient) TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
	var resp []CodeAction
	if l.Conn == nil {
		return resp, fmt.Errorf("TextDocumentCodeAction called before Initialize")
	}
	err := l.Conn.Call(ctx, "textDocument/codeAction", params, &resp)
	if err != nil {
		return resp, err
	}
	//utils.PrettyPrint(resp) // debug
	return resp, nil
}

// textDocument/implementation
func (l *Jsonrpc2LSPClient) TextDocumentImplementation(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentImplementation called before Initialize")
	}
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{
			DocumentURI: uri,
		},
		Position: Position{
			Line:      line,
			Character: character,
		},
	}
	var locations []Location
	err := l.Conn.Call(ctx, "textDocument/implementation", params, &locations)
	if err != nil {
		return []Location{}, err
	}
	return locations, nil
}

func (l *Jsonrpc2LSPClient) GetServerCapabilities() ServerCapabilities {
	return l.ServerCapabilities
}
