package git


import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"
	"strings"

	"go.temporal.io/sdk/workflow"
)

type GitCommitParams struct {
	CommitMessage string
	CommitAll     bool
}

func GitCommitActivity(ctx context.Context, envContainer env.EnvContainer, params GitCommitParams) (string, error) {
	var gitCommitOutput env.EnvRunCommandActivityOutput
	args := []string{"-c", "user.name='Sidekick'", "-c", "user.email='sidekick@side.dev'", "commit", "-m", params.CommitMessage}
	if params.CommitAll {
		args = append(args, "-a")
	}
	gitCommitOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return "", fmt.Errorf("failed to git commit: %v", err)
	}
	if gitCommitOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git commit failed: %s", gitCommitOutput.Stdout+"\n"+gitCommitOutput.Stderr)
	}
	return gitCommitOutput.Stdout, nil
}

func GitCommit(eCtx flow_action.ExecContext, commitMessage string) error {
	diff, err := gitDiffStaged(eCtx)
	if diff == "" && err == nil {
		// can't commit what ain't staged (points at head)
		return nil
	}

	commitParams := GitCommitParams{
		CommitMessage: commitMessage,
	}
	commitErr := workflow.ExecuteActivity(eCtx, GitCommitActivity, eCtx.EnvContainer, commitParams).Get(eCtx, nil)
	if commitErr != nil {
		// TODO add test for nothing to commit case
		if !strings.Contains(commitErr.Error(), "nothing to commit") {
			return fmt.Errorf("failed to commit changes: %v", commitErr)
		}
	}
	return nil
}

func gitDiffStaged(eCtx flow_action.ExecContext) (string, error) {
	var diff string
	err := workflow.ExecuteActivity(eCtx, GitDiffActivity, eCtx.EnvContainer, GitDiffParams{
		Staged: true,
	}).Get(eCtx, &diff)
	return diff, err
}
