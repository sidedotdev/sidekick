package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GoplsVersion is the pinned gopls version installed when none is available.
const GoplsVersion = "v0.21.0"

// CommandOutput is the result of a command run by a CommandRunner.
type CommandOutput struct {
	Stdout     string
	Stderr     string
	ExitStatus int
}

// CommandRunner executes a command where the language server will run, so
// tool discovery and installation happen on the same machine as the server.
type CommandRunner func(ctx context.Context, command string, args ...string) (CommandOutput, error)

// FindOrInstallGopls resolves the gopls binary wherever run executes commands,
// installing it if necessary.
func FindOrInstallGopls(ctx context.Context, run CommandRunner) (string, error) {
	// Resolve gopls to an absolute path when possible: the probe may run in a
	// login shell with a fuller PATH than the raw exec that later launches the
	// server. An absolute path works identically in both contexts.
	if out, err := run(ctx, "sh", "-c", "command -v gopls"); err == nil && out.ExitStatus == 0 {
		if goplsPath := strings.TrimSpace(out.Stdout); strings.HasPrefix(goplsPath, "/") && goplsRuns(ctx, run, goplsPath) {
			return goplsPath, nil
		}
	}

	if goplsRuns(ctx, run, "gopls") {
		return "gopls", nil
	}

	// A gopls installed by a previous session lives in the go bin directory,
	// which may not be on the non-interactive PATH, so probe its absolute path
	// before attempting a (slow) reinstall on every worker restart.
	binDir, err := goBinDir(ctx, run)
	if err != nil {
		return "", err
	}
	goplsPath := binDir + "/gopls"
	if goplsRuns(ctx, run, goplsPath) {
		return goplsPath, nil
	}

	var installErr error
	for i := 0; i < 3; i++ {
		out, err := run(ctx, "go", "install", "golang.org/x/tools/gopls@"+GoplsVersion)
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
func goplsRuns(ctx context.Context, run CommandRunner, goplsPath string) bool {
	out, err := run(ctx, goplsPath, "version")
	return err == nil && out.ExitStatus == 0
}

// goBinDir returns the directory where `go install` places binaries in the
// environment, preferring GOBIN and falling back to the first GOPATH entry.
func goBinDir(ctx context.Context, run CommandRunner) (string, error) {
	if out, err := run(ctx, "go", "env", "GOBIN"); err == nil && out.ExitStatus == 0 {
		if dir := strings.TrimSpace(out.Stdout); dir != "" {
			return dir, nil
		}
	}
	out, err := run(ctx, "go", "env", "GOPATH")
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
