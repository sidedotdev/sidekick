package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

// MergeBase returns the best common ancestor commit of two refs.
func MergeBase(ctx context.Context, envContainer env.EnvContainer, refA, refB string) (string, error) {
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"merge-base", refA, refB},
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute git merge-base: %w", err)
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("git merge-base %s %s failed with exit status %d: %s", refA, refB, output.ExitStatus, output.Stderr)
	}

	return strings.TrimSpace(output.Stdout), nil
}
