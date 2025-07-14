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

// MergeActivityResult indicates the result of a merge operation.
type MergeActivityResult struct {
	HasConflicts bool
}

// GitMergeActivity performs a git merge operation from a source branch into a target branch.
// If a worktree exists for the target branch, the merge will be performed there.
// Otherwise, a temporary checkout of the target branch will be used.
// It returns MergeActivityResult indicating if conflicts occurred, and an error if any operational failure happened.
func GitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeParams) (result MergeActivityResult, resultErr error) {
	if params.SourceBranch == "" || params.TargetBranch == "" {
		resultErr = fmt.Errorf("both source and target branches are required for merge")
		return
	}

	repoDir := envContainer.Env.GetWorkingDirectory()
	if repoDir == "" {
		resultErr = fmt.Errorf("repository directory not found in environment")
		return
	}

	worktrees, listWorktreesErr := ListWorktreesActivity(ctx, repoDir)
	if listWorktreesErr != nil {
		resultErr = fmt.Errorf("failed to list worktrees: %v", listWorktreesErr)
		return
	}

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
		mergeOutput, mergeErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "sh",
			Args:         []string{"-c", mergeCmd},
		})
		if mergeErr != nil {
			resultErr = fmt.Errorf("failed to execute merge command in worktree: %v", mergeErr)
			return
		}
		if mergeOutput.ExitStatus != 0 {
			if strings.Contains(mergeOutput.Stdout, "CONFLICT") || strings.Contains(mergeOutput.Stderr, "conflict") {
				result.HasConflicts = true
				// In a worktree, we don't need to abort the merge. The conflicted state is contained
				// within the worktree and can be inspected or cleaned up later.
				return
			}
			resultErr = fmt.Errorf("merge failed in worktree: %s", mergeOutput.Stderr)
			return
		}
		// Merge successful, no conflicts. result.HasConflicts is false (default), resultErr is nil.
		return
	}

	// No worktree found, use temporary checkout approach

	// Ensure we restore the original branch when we're done.
	// The 'resultErr' variable is the named return error for GitMergeActivity.
	// This defer can modify 'resultErr' if restoring the branch fails and no prior operational error occurred.
	defer func() {
		// the original branch of the working directory is expected to match the
		// source branch. this matches the way worktrees are created in
		// NewLocalGitWorktreeEnv
		originalBranch := params.SourceBranch
		restoreCheckoutOutput, restoreCheckoutErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "git",
			Args:         []string{"checkout", originalBranch},
		})

		// If 'resultErr' was already set by a preceding operational error in GitMergeActivity (e.g., initial checkout failure,
		// or merge command failed for non-conflict reasons), we keep that original error.
		// The restoration failure is secondary in that case.
		if resultErr == nil {
			// If the merge operation itself was clean or resulted in conflicts (resultErr == nil at this point),
			// then this restoration error takes precedence as the function's returned error.
			if restoreCheckoutErr != nil {
				resultErr = fmt.Errorf("failed to run command to restore original branch %s: %v", originalBranch, restoreCheckoutErr)
			} else if restoreCheckoutOutput.ExitStatus != 0 {
				resultErr = fmt.Errorf("failed to restore original branch %s, command stderr: %s", originalBranch, restoreCheckoutOutput.Stderr)
			}
		}
	}()

	// Checkout the target branch before merging
	checkoutOutput, checkoutErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"checkout", params.TargetBranch},
	})
	if checkoutErr != nil {
		resultErr = fmt.Errorf("failed to run command to checkout target branch %s: %v", params.TargetBranch, checkoutErr)
		return
	}
	if checkoutOutput.ExitStatus != 0 {
		resultErr = fmt.Errorf("failed to checkout target branch %s, command stderr: %s", params.TargetBranch, checkoutOutput.Stderr)
		return
	}

	// Perform the merge
	mergeOutput, mergeErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"merge", params.SourceBranch},
	})
	if mergeErr != nil {
		resultErr = fmt.Errorf("failed to execute merge command: %v", mergeErr)
		return
	}
	if mergeOutput.ExitStatus != 0 {
		if strings.Contains(mergeOutput.Stdout, "CONFLICT") || strings.Contains(mergeOutput.Stderr, "conflict") {
			result.HasConflicts = true
			// Attempt to abort the merge to clean up the repository state.
			abortOutput, abortErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: envContainer,
				Command:      "git",
				Args:         []string{"merge", "--abort"},
			})
			if abortErr != nil {
				resultErr = fmt.Errorf("merge had conflicts and failed to abort: %v", abortErr)
				return
			}
			if abortOutput.ExitStatus != 0 {
				resultErr = fmt.Errorf("merge had conflicts and failed to abort, stderr: %s", abortOutput.Stderr)
				return
			}
			// resultErr remains nil (unless defer sets it to a restore error)
			return
		}
		resultErr = fmt.Errorf("merge failed: %s", mergeOutput.Stderr)
		return
	}

	// Merge successful, no conflicts.
	// result.HasConflicts is false (default).
	// resultErr is nil (unless defer sets it to a restore error).
	return
}
