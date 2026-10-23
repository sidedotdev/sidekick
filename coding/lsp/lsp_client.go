package lsp

import (
	"context"
	"fmt"
	"os/exec"
	"sidekick/coding/lsp/core"
	"sidekick/common"
	"sidekick/env"
	"strings"
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

var ErrUnsupportedLanguage = core.ErrUnsupportedLanguage

func IsSupportedLanguage(lang string) bool {
	return core.IsSupportedLanguage(lang)
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
// installing it if necessary. A nil envContainer falls back to locating gopls
// on the host.
func findOrInstallGopls(ctx context.Context, envContainer *env.EnvContainer) (string, error) {
	if envContainer == nil || envContainer.Env == nil {
		return common.FindOrInstallGopls()
	}
	e := envContainer.Env
	return core.FindOrInstallGopls(ctx, func(ctx context.Context, command string, args ...string) (core.CommandOutput, error) {
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: command, Args: args})
		return core.CommandOutput{Stdout: out.Stdout, Stderr: out.Stderr, ExitStatus: out.ExitStatus}, err
	})
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
