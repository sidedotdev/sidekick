package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

type GitAddActivityInput struct {
	EnvContainer env.EnvContainer
	Path         string
}

func GitAddActivity(ctx context.Context, input GitAddActivityInput) error {
	// "Add all" must not stage newly introduced binary files, which are usually
	// build artifacts; if they should be tracked, the agent stages them
	// explicitly via a specific-path add, which is left as-is.
	if input.Path == "." {
		return gitAddAllExceptUntrackedBinaries(ctx, input.EnvContainer)
	}

	args := []string{"add", input.Path}
	gitAddOutput, err := runGitIndexCommandWithRetry(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       input.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	}, "")
	if err != nil {
		return fmt.Errorf("failed to git add: %v", err)
	}
	if gitAddOutput.ExitStatus != 0 {
		return fmt.Errorf("git add failed: %s", gitAddOutput.Stdout+"\n"+gitAddOutput.Stderr)
	}
	return nil
}

// gitAddAllExceptUntrackedBinaries stages all tracked changes (modifications
// and deletions) along with non-binary untracked files, while leaving untracked
// binary files untracked on disk. Already-tracked binaries are unaffected.
func gitAddAllExceptUntrackedBinaries(ctx context.Context, envContainer env.EnvContainer) error {
	untracked, err := listUntrackedFiles(ctx, envContainer, nil)
	if err != nil {
		return err
	}
	nonBinary, binary, err := partitionUntrackedBinaries(ctx, envContainer, untracked)
	if err != nil {
		return err
	}

	// `git add -u` stages modifications and deletions of tracked files without
	// adding untracked files, letting us selectively add non-binary ones below.
	if err := runGitAdd(ctx, envContainer, []string{"-u"}); err != nil {
		return err
	}
	if len(nonBinary) > 0 {
		if err := runGitAdd(ctx, envContainer, append([]string{"--"}, nonBinary...)); err != nil {
			return err
		}
	}
	if len(binary) > 0 {
		log.Info().Strs("files", binary).Msg("skipped staging untracked binary files")
	}
	return nil
}

func runGitAdd(ctx context.Context, envContainer env.EnvContainer, addArgs []string) error {
	args := append([]string{"add"}, addArgs...)
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	if err != nil {
		return fmt.Errorf("failed to git add: %v", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("git add failed: %s", output.Stdout+"\n"+output.Stderr)
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
