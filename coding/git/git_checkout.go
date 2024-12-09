package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

type GitCheckoutActivityInput struct {
	EnvContainer env.EnvContainer
	Commit       string
	Path         string
}

func GitCheckoutActivity(ctx context.Context, input GitCheckoutActivityInput) error {
	args := []string{"checkout", input.Commit, input.Path}
	gitCheckoutOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       input.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return fmt.Errorf("failed to git checkout HEAD: %v", err)
	}
	if gitCheckoutOutput.ExitStatus != 0 {
		return fmt.Errorf("git checkout HEAD failed: %s", gitCheckoutOutput.Stdout+"\n"+gitCheckoutOutput.Stderr)
	}
	return nil
}

func GitCheckoutHeadAll(eCtx flow_action.ExecContext) error {
	var gitCheckoutOutput env.EnvRunCommandOutput
	input := GitCheckoutActivityInput{
		EnvContainer: *eCtx.EnvContainer,
		Commit:       "HEAD",
		Path:         ".",
	}
	err := workflow.ExecuteActivity(eCtx, GitCheckoutActivity, input).Get(eCtx, &gitCheckoutOutput)
	if err != nil {
		return fmt.Errorf("failed to git checkout HEAD: %v", err)
	}

	if gitCheckoutOutput.ExitStatus != 0 {
		return fmt.Errorf("git checkout HEAD failed: %v", gitCheckoutOutput.Stderr)
	}
	return nil
}
