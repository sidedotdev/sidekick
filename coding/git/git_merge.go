package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

type GitMergeParams struct {
	SourceBranch  string // The branch to merge from (typically the worktree branch)
	TargetBranch  string // The branch to merge into (typically the base branch)
	CommitMessage string // Required for basic workflows to create initial commit
}

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// If a worktree exists for the target branch, the merge will be performed there.
// Otherwise, a temporary checkout of the target branch will be used.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) error {
	if params.SourceBranch == "" || params.TargetBranch == "" {
		return fmt.Errorf("both source and target branches are required for merge")
	}

	// Get repo directory from environment
	repoDir := envContainer.Env.GetWorkingDirectory()
	if repoDir == "" {
		return fmt.Errorf("repository directory not found in environment")
	}

	// List worktrees to find if target branch has one
	worktrees, err := ListWorktreesActivity(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("failed to list worktrees: %v", err)
	}

	// Find worktree for target branch if it exists
	var targetWorktree *GitWorktree
	for _, wt := range worktrees {
		if wt.Branch == params.TargetBranch {
			targetWorktree = &wt
			break
		}
	}

	if targetWorktree != nil {
		// Use worktree path for merge
		mergeCmd := fmt.Sprintf("cd %s && git merge %s", targetWorktree.Path, params.SourceBranch)
		mergeOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "sh",
			Args:         []string{"-c", mergeCmd},
		})
		if err != nil {
			return fmt.Errorf("failed to execute merge command in worktree: %v", err)
		}
		if mergeOutput.ExitStatus != 0 && !strings.Contains(mergeOutput.Stderr, "CONFLICT") {
			return fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
		}
		return nil
	}

	// No worktree found, use temporary checkout approach
	// Save current branch to restore later
	currentBranch, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"rev-parse", "--abbrev-ref", "HEAD"},
	})
	if err != nil {
		return fmt.Errorf("failed to get current branch: %v", err)
	}

	// Switch to target branch
	checkoutOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"checkout", params.TargetBranch},
	})
	if err != nil || checkoutOutput.ExitStatus != 0 {
		return fmt.Errorf("failed to checkout target branch: %v", err)
	}

	// Ensure we restore the original branch when we're done
	defer func() {
		_, _ = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "git",
			Args:         []string{"checkout", strings.TrimSpace(currentBranch.Stdout)},
		})
	}()

	// Perform the merge
	mergeOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"merge", params.SourceBranch},
	})
	if err != nil {
		return fmt.Errorf("failed to execute merge command: %v", err)
	}
	if mergeOutput.ExitStatus != 0 && !strings.Contains(mergeOutput.Stderr, "CONFLICT") {
		return fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
	}

	return nil
}
