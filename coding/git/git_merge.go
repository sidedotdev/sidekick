package git

import (
	"context"
	"fmt"
	"sidekick/env"
)

type GitMergeParams struct {
	SourceBranch    string // The branch to merge from (typically the worktree branch)
	TargetBranch    string // The branch to merge into (typically the base branch)
	CommitMessage   string // Required for basic workflows to create initial commit
	OriginalRepoDir string // Path to original repo directory (not worktree)
}

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// For basic workflows, it first creates a commit of any changes. The merge is performed
// in the original repo directory, not the worktree directory.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) error {
	if params.SourceBranch == "" || params.TargetBranch == "" {
		return fmt.Errorf("both source and target branches are required for merge")
	}
	if params.OriginalRepoDir == "" {
		return fmt.Errorf("original repo directory is required for merge")
	}

	// Switch to target branch in original repo
	checkoutOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: params.OriginalRepoDir,
		Command:            "git",
		Args:               []string{"checkout", params.TargetBranch},
	})
	if err != nil || checkoutOutput.ExitStatus != 0 {
		return fmt.Errorf("failed to checkout target branch: %v", err)
	}

	// Perform the merge
	mergeOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: params.OriginalRepoDir,
		Command:            "git",
		Args:               []string{"merge", params.SourceBranch},
	})
	if err != nil {
		return fmt.Errorf("failed to execute merge command: %v", err)
	}
	if mergeOutput.ExitStatus != 0 {
		return fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
	}

	return nil
}
