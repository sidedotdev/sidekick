package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

func WriteTreeActivity(ctx context.Context, envContainer env.EnvContainer) (string, error) {
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"write-tree"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to run git write-tree: %v", err)
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("git write-tree failed: %s", output.Stdout+"\n"+output.Stderr)
	}
	return strings.TrimSpace(output.Stdout), nil
}
