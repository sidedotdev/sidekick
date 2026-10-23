package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

// ReadWriteCloser is the stdio transport of a language server process.
type ReadWriteCloser struct {
	io.Reader
	io.WriteCloser
	command  *exec.Cmd
	waited   <-chan error
	once     sync.Once
	closeErr error
}

func (rwc *ReadWriteCloser) Close() error {
	rwc.once.Do(func() {
		recordError := func(err error) {
			if !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
				rwc.closeErr = errors.Join(rwc.closeErr, err)
			}
		}
		recordError(rwc.WriteCloser.Close())
		if closer, ok := rwc.Reader.(io.Closer); ok {
			recordError(closer.Close())
		}
		if rwc.command != nil {
			select {
			case <-rwc.waited:
			case <-time.After(2 * time.Second):
				_ = rwc.command.Process.Kill()
				<-rwc.waited
			}
		}
	})
	return rwc.closeErr
}

// Command returns the process backing the transport, if any.
func (rwc *ReadWriteCloser) Command() *exec.Cmd {
	return rwc.command
}

// StartServerTransport starts cmd and uses its stdio as a transport. Closing
// the transport reaps the process.
func StartServerTransport(cmd *exec.Cmd) (*ReadWriteCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cmd.StdinPipe() failed: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("cmd.StdoutPipe() failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("cmd.Start() failed: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	return &ReadWriteCloser{
		Reader: stdout, WriteCloser: stdin, command: cmd, waited: waited,
	}, nil
}

// JSONRPCClient speaks LSP over a transport supplied by its owner, which
// decides where the server runs.
type JSONRPCClient struct {
	Conn               *jsonrpc2.Conn
	ServerCapabilities ServerCapabilities
	transport          io.ReadWriteCloser
}

type noopHandler struct{}

func (noopHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	//fmt.Printf("\nnoopHandler.Handle called with req: %v\n", req)
}

// Connect initializes the server over transport, taking ownership of it.
func (l *JSONRPCClient) Connect(ctx context.Context, transport io.ReadWriteCloser, params InitializeParams) (InitializeResponse, error) {
	l.transport = transport
	// An activity's cancellation must not tear down a reusable connection.
	l.Conn = jsonrpc2.NewConn(context.WithoutCancel(ctx), jsonrpc2.NewBufferedStream(transport, jsonrpc2.VSCodeObjectCodec{}), &noopHandler{})

	var resp InitializeResponse
	if err := l.Conn.Call(ctx, "initialize", &params, &resp); err != nil {
		_ = l.Close()
		return InitializeResponse{}, fmt.Errorf("initialize call failed: %w", err)
	}
	if err := l.Conn.Notify(ctx, "initialized", &struct{}{}); err != nil {
		_ = l.Close()
		return InitializeResponse{}, fmt.Errorf("initialized notification failed: %w", err)
	}
	l.ServerCapabilities = resp.Capabilities
	return resp, nil
}

// textDocument/definition
func (l *JSONRPCClient) TextDocumentDefinition(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentDefinition called before Initialize")
	}
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{
			URI: uri,
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
func (l *JSONRPCClient) TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentReferences called before Initialize")
	}
	params := ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{
				URI: uri,
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
func (l *JSONRPCClient) TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
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
func (l *JSONRPCClient) TextDocumentImplementation(ctx context.Context, uri string, line int, character int) ([]Location, error) {
	if l.Conn == nil {
		return []Location{}, fmt.Errorf("TextDocumentImplementation called before Initialize")
	}
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{
			URI: uri,
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

// textDocument/prepareCallHierarchy
func (l *JSONRPCClient) PrepareCallHierarchy(ctx context.Context, uri string, line int, character int) ([]CallHierarchyItem, error) {
	if l.Conn == nil {
		return []CallHierarchyItem{}, fmt.Errorf("PrepareCallHierarchy called before Initialize")
	}
	params := CallHierarchyPrepareParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{
				URI: uri,
			},
			Position: Position{
				Line:      line,
				Character: character,
			},
		},
	}
	var items []CallHierarchyItem
	err := l.Conn.Call(ctx, "textDocument/prepareCallHierarchy", params, &items)
	if err != nil {
		return []CallHierarchyItem{}, err
	}
	return items, nil
}

// callHierarchy/incomingCalls
func (l *JSONRPCClient) CallHierarchyIncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error) {
	if l.Conn == nil {
		return []CallHierarchyIncomingCall{}, fmt.Errorf("CallHierarchyIncomingCalls called before Initialize")
	}
	params := CallHierarchyIncomingCallsParams{
		Item: item,
	}
	var calls []CallHierarchyIncomingCall
	err := l.Conn.Call(ctx, "callHierarchy/incomingCalls", params, &calls)
	if err != nil {
		return []CallHierarchyIncomingCall{}, err
	}
	return calls, nil
}

func (l *JSONRPCClient) GetServerCapabilities() ServerCapabilities {
	return l.ServerCapabilities
}

// textDocument/didOpen notification
func (l *JSONRPCClient) TextDocumentDidOpen(ctx context.Context, params DidOpenTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidOpen called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didOpen", params)
}

// textDocument/didChange notification
func (l *JSONRPCClient) TextDocumentDidChange(ctx context.Context, params DidChangeTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidChange called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didChange", params)
}

// textDocument/didSave notification
func (l *JSONRPCClient) TextDocumentDidSave(ctx context.Context, params DidSaveTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidSave called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didSave", params)
}

// textDocument/didClose notification
func (l *JSONRPCClient) TextDocumentDidClose(ctx context.Context, params DidCloseTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidClose called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didClose", params)
}

func (l *JSONRPCClient) Close() error {
	var result error
	if l.Conn != nil {
		result = l.Conn.Close()
		if errors.Is(result, jsonrpc2.ErrClosed) {
			result = nil
		}
	}
	if l.transport != nil {
		result = errors.Join(result, l.transport.Close())
	}
	return result
}

func (l *JSONRPCClient) IsConnected() bool {
	if l.Conn == nil {
		return false
	}
	select {
	case <-l.Conn.DisconnectNotify():
		return false
	default:
		return true
	}
}
