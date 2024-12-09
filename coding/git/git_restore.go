package git

import (
	"context"
	"fmt"
	"sidekick/env"
)

func GitRestoreActivity(ctx context.Context, envContainer env.EnvContainer, filePath string) error {
	args := []string{"restore", filePath}
	gitRestoreOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return fmt.Errorf("failed to git restore: %v", err)
	}
	if gitRestoreOutput.ExitStatus != 0 {
		return fmt.Errorf("git restore failed: %s", gitRestoreOutput.Stdout+"\n"+gitRestoreOutput.Stderr)
	}
	return nil
}
