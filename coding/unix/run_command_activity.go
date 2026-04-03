package unix

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sidekick/logger"
	"sidekick/utils"
	"strings"
	"syscall"
	"time"
)

type RunCommandActivityInput struct {
	WorkingDir string   `json:"workingDir"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	EnvVars    []string `json:"envVars"`
}

type RunCommandActivityOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitStatus int    `json:"exitStatus"`
}

func RunCommandActivity(ctx context.Context, input RunCommandActivityInput) (RunCommandActivityOutput, error) {
	if input.WorkingDir == "" {
		return RunCommandActivityOutput{}, errors.New("WorkingDir must be provided")
	}

	if input.Command == "" {
		return RunCommandActivityOutput{}, errors.New("Command must be provided")
	}

	cmd := exec.CommandContext(ctx, input.Command, input.Args...)
	cmd.Dir = input.WorkingDir

	filteredEnvs := utils.Filter(os.Environ(), func(envVar string) bool {
		isSide := strings.HasPrefix(envVar, "SIDE_")
		if isSide {
			l := logger.Get()
			l.Debug().Msg("Filtered envVar with \"SIDE_\" prefix: " + envVar)
		}
		return !isSide
	})

	cmd.Env = append(filteredEnvs, input.EnvVars...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// WaitDelay bounds how long Wait blocks for I/O pipes to close after the
	// process exits. Background children (& / nohup) inherit the pipes and
	// would otherwise block Wait indefinitely.
	cmd.WaitDelay = 100 * time.Millisecond

	runErr := cmd.Run()

	if err != nil && ctx.Err() != nil {
		return RunCommandActivityOutput{
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
			return RunCommandActivityOutput{}, runErr
		}
	}

	output := RunCommandActivityOutput{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitStatus: exitStatus,
	}

	return output, nil
}
