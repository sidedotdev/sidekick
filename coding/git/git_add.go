package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

type GitAddActivityInput struct {
	EnvContainer env.EnvContainer
	Path         string
}

func GitAddActivity(ctx context.Context, input GitAddActivityInput) error {
	args := []string{"add", input.Path}
	gitAddOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       input.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return fmt.Errorf("failed to git add: %v", err)
	}
	if gitAddOutput.ExitStatus != 0 {
		return fmt.Errorf("git add failed: %s", gitAddOutput.Stdout+"\n"+gitAddOutput.Stderr)
	}
	return nil
}

func GitAddAll(eCtx flow_action.ExecContext) error {
	var gitAddOutput env.EnvRunCommandOutput
	input := GitAddActivityInput{EnvContainer: *eCtx.EnvContainer, Path: "."}
	err := workflow.ExecuteActivity(eCtx, GitAddActivity, input).Get(eCtx, &gitAddOutput)
	if err != nil {
		return fmt.Errorf("failed to git add all: %v", err)
	}

	if gitAddOutput.ExitStatus != 0 {
		return fmt.Errorf("git add all failed: %v", gitAddOutput.Stderr)
	}
	return nil
}
