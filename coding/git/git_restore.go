package git

import (
	"context"
	"fmt"
	"sidekick/env"
)

func (a *Activities) Restore(ctx context.Context, envContainer env.EnvContainer, filePath string) error {
	args := []string{"restore", filePath}
	gitRestoreOutput, err := a.Env.RunCommand(ctx, env.EnvRunCommandActivityInput{
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
