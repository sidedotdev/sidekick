package lsp

import (
	"context"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEnv implements env.Env by embedding the interface (unused methods panic
// if called) and overriding only what the gopls command builder exercises.
type fakeEnv struct {
	env.Env
	workDir    string
	runCommand func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error)
	readFile   func(ctx context.Context, path string) ([]byte, error)
}

func (f *fakeEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	return f.runCommand(ctx, input)
}

func (f *fakeEnv) GetWorkingDirectory() string { return f.workDir }

func (f *fakeEnv) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return f.readFile(ctx, path)
}

// fakeSSHEnv additionally satisfies env.SSHCapableEnv so the server is launched
// inside the environment over SSH.
type fakeSSHEnv struct {
	*fakeEnv
	sshArgs []string
}

func (f *fakeSSHEnv) SSHArgs(ctx context.Context) ([]string, error) { return f.sshArgs, nil }

// goplsPresent simulates an environment where gopls is already on PATH.
func goplsPresent(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	if input.Command == "gopls" {
		return env.EnvRunCommandOutput{ExitStatus: 0, Stdout: "gopls v0.0.0"}, nil
	}
	return env.EnvRunCommandOutput{ExitStatus: 127}, nil
}

func TestLSPServerCommand_SSHCapableEnvRunsGoplsOverSSH(t *testing.T) {
	t.Parallel()
	sshEnv := &fakeSSHEnv{
		fakeEnv: &fakeEnv{runCommand: goplsPresent},
		sshArgs: []string{"-F", "/dev/null", "myhost.devpod"},
	}

	cmd, err := lspServerCommand(context.Background(), "golang", &env.EnvContainer{Env: sshEnv})
	require.NoError(t, err)

	require.Equal(t, "ssh", cmd.Args[0])
	assert.Equal(t, []string{"-F", "/dev/null", "myhost.devpod"}, cmd.Args[1:len(cmd.Args)-1])

	remoteCmd := cmd.Args[len(cmd.Args)-1]
	assert.True(t, strings.HasPrefix(remoteCmd, "'gopls'"),
		"remote command should launch gopls, got %q", remoteCmd)
	assert.Contains(t, remoteCmd, "-remote=auto")
}

func TestLSPServerCommand_LocalEnvRunsGoplsDirectly(t *testing.T) {
	t.Parallel()
	localEnv := &fakeEnv{runCommand: goplsPresent}

	cmd, err := lspServerCommand(context.Background(), "golang", &env.EnvContainer{Env: localEnv})
	require.NoError(t, err)

	assert.Equal(t, "gopls", cmd.Args[0])
	assert.Contains(t, cmd.Args, "-remote=auto")
}

func TestFindOrInstallGopls_InstallsWhenMissing(t *testing.T) {
	t.Parallel()
	var installed bool
	e := &fakeEnv{runCommand: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
		switch {
		case input.Command == "gopls":
			return env.EnvRunCommandOutput{ExitStatus: 127, Stderr: "gopls: not found"}, nil
		case input.Command == "go" && len(input.Args) > 0 && input.Args[0] == "install":
			installed = true
			return env.EnvRunCommandOutput{ExitStatus: 0}, nil
		case input.Command == "go" && len(input.Args) >= 2 && input.Args[1] == "GOBIN":
			return env.EnvRunCommandOutput{ExitStatus: 0, Stdout: "\n"}, nil
		case input.Command == "go" && len(input.Args) >= 2 && input.Args[1] == "GOPATH":
			return env.EnvRunCommandOutput{ExitStatus: 0, Stdout: "/home/dev/go\n"}, nil
		}
		return env.EnvRunCommandOutput{ExitStatus: 1}, nil
	}}

	path, err := findOrInstallGopls(context.Background(), &env.EnvContainer{Env: e})
	require.NoError(t, err)
	assert.True(t, installed, "expected gopls install to be attempted")
	assert.Equal(t, "/home/dev/go/bin/gopls", path)
}
