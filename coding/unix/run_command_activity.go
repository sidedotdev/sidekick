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

	err := cmd.Run()

	exitStatus := 0
	// Check if there's an error and if it's an ExitError.
	if err != nil {
		exitError, ok := err.(*exec.ExitError)
		if !ok {
			// If it's not an ExitError, return it as an actual error.
			return RunCommandActivityOutput{}, err
		}
		// If it's an ExitError, get the exit status.
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			exitStatus = status.ExitStatus()
		}
		err = nil
	}
	output := RunCommandActivityOutput{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitStatus: exitStatus,
	}

	return output, err
}
