package localio

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

type RunCommandInput struct {
	WorkingDir string   `json:"workingDir"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	EnvVars    []string `json:"envVars"`
}

type RunCommandOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitStatus int    `json:"exitStatus"`
}

func RunCommand(ctx context.Context, input RunCommandInput) (RunCommandOutput, error) {
	if input.WorkingDir == "" {
		return RunCommandOutput{}, errors.New("WorkingDir must be provided")
	}

	if input.Command == "" {
		return RunCommandOutput{}, errors.New("Command must be provided")
	}

	cmd := exec.CommandContext(ctx, input.Command, input.Args...)
	cmd.Dir = input.WorkingDir

	// Worker-only settings must not leak into child processes.
	filteredEnvs := slices.DeleteFunc(os.Environ(), func(envVar string) bool {
		return strings.HasPrefix(envVar, "SIDE_")
	})
	cmd.Env = append(filteredEnvs, input.EnvVars...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// WaitDelay bounds how long Wait blocks for I/O pipes to close after the
	// process exits. Background children (& / nohup) inherit the pipes and
	// would otherwise block Wait indefinitely.
	cmd.WaitDelay = 100 * time.Millisecond

	// A new session has no controlling terminal, so interactive prompts (e.g.
	// sudo's password prompt) fail fast instead of touching the worker's
	// terminal. When the worker runs in a background process group, terminal
	// access sends SIGTTOU/SIGTTIN to the whole group, stopping the worker.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	runErr := cmd.Run()

	if runErr != nil && ctx.Err() != nil {
		return RunCommandOutput{
			Stdout:     stdout.String(),
			Stderr:     stderr.String(),
			ExitStatus: -1,
		}, ctx.Err()
	}

	exitStatus := 0
	if runErr != nil {
		if errors.Is(runErr, exec.ErrWaitDelay) {
			// Process exited successfully but background children held pipes open.
		} else if exitError, ok := runErr.(*exec.ExitError); ok {
			if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
				exitStatus = status.ExitStatus()
			}
		} else {
			return RunCommandOutput{}, runErr
		}
	}

	output := RunCommandOutput{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitStatus: exitStatus,
	}

	return output, nil
}
