package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

var ErrUnsupportedLanguage = errors.New("unsupported language")

func IsSupportedLanguage(lang string) bool {
	switch lang {
	case "golang":
		return true
	default:
		return false
	}
}

// LocalServerClient runs a language server as a child process of the current
// process, for use where the repository is on the local filesystem.
type LocalServerClient struct {
	JSONRPCClient
	LanguageName string
	// Env is the environment for the server and for tool discovery; nil
	// inherits the current process environment.
	Env []string
}

// NewLocalServerProvider returns a Sessions client provider whose clients
// launch language servers locally with the given environment.
func NewLocalServerProvider(environ []string) func(language string) LSPClient {
	return func(language string) LSPClient {
		return &LocalServerClient{LanguageName: language, Env: environ}
	}
}

func (c *LocalServerClient) run(ctx context.Context, command string, args ...string) (CommandOutput, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = c.Env
	stdout, err := cmd.Output()
	output := CommandOutput{Stdout: string(stdout)}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.Stderr = string(exitErr.Stderr)
		output.ExitStatus = exitErr.ExitCode()
		return output, nil
	}
	return output, err
}

func (c *LocalServerClient) Initialize(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
	if !IsSupportedLanguage(c.LanguageName) {
		return InitializeResponse{}, fmt.Errorf("%w: %s", ErrUnsupportedLanguage, c.LanguageName)
	}
	goplsPath, err := FindOrInstallGopls(ctx, c.run)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("gopls failure: %w", err)
	}
	// A stdio server belongs to its client. A shared daemon would retain
	// workspace graphs after cache eviction and escape the owner's lifetime.
	cmd := exec.Command(goplsPath)
	cmd.Env = c.Env
	transport, err := StartServerTransport(cmd)
	if err != nil {
		return InitializeResponse{}, fmt.Errorf("gopls failure: %w", err)
	}
	return c.Connect(ctx, transport, params)
}