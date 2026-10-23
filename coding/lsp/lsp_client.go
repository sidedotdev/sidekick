package lsp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sidekick/coding/lsp/core"
	"sidekick/common"
	"sidekick/env"
	"strings"
	"time"
)

type LSPClient = core.LSPClient

type Jsonrpc2LSPClient struct {
	core.JSONRPCClient
	LanguageName string
	// EnvContainer determines where the language server runs. For SSH-capable
	// environments (e.g. a dev container) the server is launched inside the
	// environment over SSH so it operates on the environment's filesystem;
	// otherwise it runs locally. A nil value runs the server locally.
	EnvContainer *env.EnvContainer
}

type ReadWriteCloser = core.ReadWriteCloser

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
	return startServerTransport(cmd)
}

var startServerTransport = core.StartServerTransport

// A stdio server belongs to its client. A shared daemon would retain workspace
// graphs after cache eviction and escape the owner's lifetime bound.
var goplsServerArgs = []string{}

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
		env.HoldReverseForwards(ctx, sshEnv)
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
		// launched the same way to find `go` and build workspace views.
		// Other SSH envs keep a raw exec:
		// their images may provide only POSIX sh, and their sshd environment
		// already matches the one RunCommand probed gopls under.
		if envContainer.Env.GetType() == env.EnvTypeModal {
			remoteCommand = "bash -lc " + lspShellQuote(remoteCommand)
		}
		remoteCommand, err = env.ManagedBackgroundCommand(remoteCommand)
		if err != nil {
			return nil, fmt.Errorf("prepare managed analysis command: %w", err)
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

func (l *Jsonrpc2LSPClient) Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	release, err := l.BeginActivity(ctx, cancel)
	if err != nil {
		return InitializeResponse{}, err
	}
	defer release()
	rwc, err := lspServerStdioReadWriteCloser(ctx, l.LanguageName, l.EnvContainer)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("gopls failure: %w", err)
	}
	return l.Connect(ctx, rwc, params)
}

func (l *Jsonrpc2LSPClient) SetEnvContainer(container *env.EnvContainer) {
	l.EnvContainer = container
}

func (l *Jsonrpc2LSPClient) BeginActivity(ctx context.Context, onLost func(error)) (func(), error) {
	if l.EnvContainer == nil {
		return func() {}, ctx.Err()
	}
	return env.BeginActivity(ctx, l.EnvContainer.Env, onLost)
}
