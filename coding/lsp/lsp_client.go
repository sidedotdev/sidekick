package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sidekick/common"
	"sidekick/env"
	"strings"
	"time"

	"github.com/sourcegraph/jsonrpc2"
)

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

type Jsonrpc2LSPClient struct {
	Conn               *jsonrpc2.Conn
	ServerCapabilities ServerCapabilities
	LanguageName       string
	// EnvContainer determines where the language server runs. For SSH-capable
	// environments (e.g. a dev container) the server is launched inside the
	// environment over SSH so it operates on the environment's filesystem;
	// otherwise it runs locally. A nil value runs the server locally.
	EnvContainer *env.EnvContainer
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

func IsSupportedLanguage(lang string) bool {
	switch lang {
	case "golang":
		return true
	default:
		return false
	}
}

func lspServerStdioReadWriteCloser(ctx context.Context, languageName string, envContainer *env.EnvContainer) (*ReadWriteCloser, error) {
	cmd, err := lspServerCommand(ctx, languageName, envContainer)
	if err != nil {
		return nil, err
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

// goplsServerArgs are the flags used to launch gopls as a stdio LSP server. The
// -remote=auto flag makes gopls attach to (or spawn) a shared daemon, so state
// is retained across reconnects and repeated connections do not leak servers.
var goplsServerArgs = []string{"-remote=auto", "-logfile=auto", "-debug=:0", "-remote.debug=:0", "-rpc.trace", "-remote.listen.timeout=0"}

// lspServerCommand builds the (unstarted) command whose stdio is used as the
// JSON-RPC transport to the language server. When envContainer wraps an
// SSH-capable environment, the server is launched inside that environment over
// SSH (without a PTY, so the pipe stays a clean binary channel) and thus
// operates on the environment's filesystem. Otherwise the server runs locally.
func lspServerCommand(ctx context.Context, languageName string, envContainer *env.EnvContainer) (*exec.Cmd, error) {
	if languageName != "golang" {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedLanguage, languageName)
	}

	goplsPath, err := findOrInstallGopls(ctx, envContainer)
	if err != nil {
		return nil, fmt.Errorf("failed to find or install gopls: %w", err)
	}

	if sshEnv, ok := sshCapableEnv(envContainer); ok {
		sshArgs, err := sshEnv.SSHArgs(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get SSH args for env: %w", err)
		}
		remoteParts := append([]string{goplsPath}, goplsServerArgs...)
		quoted := make([]string, len(remoteParts))
		for i, part := range remoteParts {
			quoted[i] = lspShellQuote(part)
		}
		remoteCommand := strings.Join(quoted, " ")
		// Modal sandboxes expose the go toolchain on PATH only in a login
		// shell (ModalEnv.RunCommand itself wraps commands in `bash -lc`, and
		// Modal images are Debian-based so bash is guaranteed). gopls must be
		// launched the same way, or the daemon it spawns cannot find `go` and
		// fails to build workspace views. Other SSH envs keep a raw exec:
		// their images may provide only POSIX sh, and their sshd environment
		// already matches the one RunCommand probed gopls under.
		if envContainer.Env.GetType() == env.EnvTypeModal {
			remoteCommand = "bash -lc " + lspShellQuote(remoteCommand)
		}
		args := append(append([]string{}, sshArgs...), remoteCommand)
		return exec.Command("ssh", args...), nil
	}

	return exec.Command(goplsPath, goplsServerArgs...), nil
}

// sshCapableEnv reports whether the env should launch the language server inside
// itself over SSH, returning the SSH-capable env when so.
func sshCapableEnv(envContainer *env.EnvContainer) (env.SSHCapableEnv, bool) {
	if envContainer == nil || envContainer.Env == nil {
		return nil, false
	}
	sshEnv, ok := envContainer.Env.(env.SSHCapableEnv)
	return sshEnv, ok
}

// lspShellQuote wraps a string in single quotes, escaping any embedded single
// quotes for safe interpolation into the remote POSIX shell command line.
func lspShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// findOrInstallGopls resolves the gopls binary within the given environment,
// installing it if necessary. It relies on env.RunCommand, so the same logic
// works for both local and remote (e.g. dev container) environments. A nil
// envContainer falls back to locating gopls on the host.
func findOrInstallGopls(ctx context.Context, envContainer *env.EnvContainer) (string, error) {
	if envContainer == nil || envContainer.Env == nil {
		return common.FindOrInstallGopls()
	}
	e := envContainer.Env

	// Resolve gopls to an absolute path when possible: the probe runs through
	// env.RunCommand (which some envs, e.g. Modal, wrap in a login shell with
	// a fuller PATH), while the LSP server is later launched via a raw SSH
	// exec whose minimal PATH may lack the go bin directory. An absolute path
	// works identically in both contexts.
	if out, err := e.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "sh", Args: []string{"-c", "command -v gopls"},
	}); err == nil && out.ExitStatus == 0 {
		if goplsPath := strings.TrimSpace(out.Stdout); strings.HasPrefix(goplsPath, "/") && goplsRuns(ctx, e, goplsPath) {
			return goplsPath, nil
		}
	}

	if goplsRuns(ctx, e, "gopls") {
		return "gopls", nil
	}

	// A gopls installed by a previous session lives in the go bin directory,
	// which may not be on the non-interactive PATH, so probe its absolute path
	// before attempting a (slow) reinstall on every worker restart.
	binDir, err := goBinDir(ctx, e)
	if err != nil {
		return "", err
	}
	goplsPath := binDir + "/gopls"
	if goplsRuns(ctx, e, goplsPath) {
		return goplsPath, nil
	}

	var installErr error
	for i := 0; i < 3; i++ {
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{
			Command: "go",
			Args:    []string{"install", "golang.org/x/tools/gopls@" + common.GoplsVersion},
		})
		if err == nil && out.ExitStatus == 0 {
			return goplsPath, nil
		}
		if err != nil {
			installErr = err
		} else {
			installErr = fmt.Errorf("go install gopls exited with status %d: %s", out.ExitStatus, out.Stderr)
		}
		if i < 2 {
			time.Sleep(time.Second)
		}
	}
	return "", installErr
}

// goplsRuns reports whether the gopls binary at goplsPath is executable in the
// environment.
func goplsRuns(ctx context.Context, e env.Env, goplsPath string) bool {
	out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: goplsPath, Args: []string{"version"}})
	return err == nil && out.ExitStatus == 0
}

// goBinDir returns the directory where `go install` places binaries in the
// environment, preferring GOBIN and falling back to the first GOPATH entry.
func goBinDir(ctx context.Context, e env.Env) (string, error) {
	if out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "go", Args: []string{"env", "GOBIN"}}); err == nil && out.ExitStatus == 0 {
		if dir := strings.TrimSpace(out.Stdout); dir != "" {
			return dir, nil
		}
	}
	out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "go", Args: []string{"env", "GOPATH"}})
	if err != nil {
		return "", err
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("go env GOPATH exited with status %d: %s", out.ExitStatus, out.Stderr)
	}
	gopath := strings.TrimSpace(out.Stdout)
	if gopath == "" {
		return "", fmt.Errorf("could not determine go bin directory")
	}
	if idx := strings.IndexByte(gopath, ':'); idx >= 0 {
		gopath = gopath[:idx]
	}
	return gopath + "/bin", nil
}

type noopHandler struct{}

func (noopHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	//fmt.Printf("\nnoopHandler.Handle called with req: %v\n", req)
}

// TODO make the ReadWriteCloser a parameter, so that multiple different language servers can be supported
func (l *Jsonrpc2LSPClient) Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
	// start lsp server (if needed) and connect to it
	rwc, err := lspServerStdioReadWriteCloser(ctx, l.LanguageName, l.EnvContainer)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("gopls failure: %w", err)
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
func (l *Jsonrpc2LSPClient) TextDocumentReferences(ctx context.Context, uri string, line int, character int) ([]Location, error) {
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
func (l *Jsonrpc2LSPClient) PrepareCallHierarchy(ctx context.Context, uri string, line int, character int) ([]CallHierarchyItem, error) {
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
func (l *Jsonrpc2LSPClient) CallHierarchyIncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error) {
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

func (l *Jsonrpc2LSPClient) GetServerCapabilities() ServerCapabilities {
	return l.ServerCapabilities
}

// textDocument/didOpen notification
func (l *Jsonrpc2LSPClient) TextDocumentDidOpen(ctx context.Context, params DidOpenTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidOpen called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didOpen", params)
}

// textDocument/didChange notification
func (l *Jsonrpc2LSPClient) TextDocumentDidChange(ctx context.Context, params DidChangeTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidChange called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didChange", params)
}

// textDocument/didSave notification
func (l *Jsonrpc2LSPClient) TextDocumentDidSave(ctx context.Context, params DidSaveTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidSave called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didSave", params)
}

// textDocument/didClose notification
func (l *Jsonrpc2LSPClient) TextDocumentDidClose(ctx context.Context, params DidCloseTextDocumentParams) error {
	if l.Conn == nil {
		return fmt.Errorf("TextDocumentDidClose called before Initialize")
	}
	return l.Conn.Notify(ctx, "textDocument/didClose", params)
}
