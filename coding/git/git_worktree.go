package git

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/env"
	"strings"
)

// CleanupWorktreeActivity removes a git worktree and deletes the associated branch.
// Before deletion, it creates an archive tag with format "archive/<branchName>" pointing to the branch.
// This should be called after successful merges to clean up temporary worktrees.
// The function must be run from within the worktree directory that needs to be removed.
func CleanupWorktreeActivity(ctx context.Context, envContainer env.EnvContainer, worktreePath, branchName, archiveMessage string) error {
	if branchName == "" {
		return fmt.Errorf("branch name is required for cleanup")
	}

	// First, checkout the HEAD commit SHA to detach from the branch
	// This is necessary because we can't delete a branch that is currently checked out
	headResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"rev-parse", "HEAD"},
	})
	if err != nil {
		return fmt.Errorf("failed to get HEAD commit SHA: %v", err)
	}
	if headResult.ExitStatus != 0 {
		return fmt.Errorf("failed to get HEAD commit SHA: %s", headResult.Stderr)
	}

	headSHA := strings.TrimSpace(headResult.Stdout)
	checkoutResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"checkout", headSHA},
	})
	if err != nil {
		return fmt.Errorf("failed to checkout HEAD commit: %v", err)
	}
	if checkoutResult.ExitStatus != 0 {
		return fmt.Errorf("failed to checkout HEAD commit %s: %s", headSHA, checkoutResult.Stderr)
	}

	// Create archive tag before deleting the branch
	// Try with incrementing suffix if tag already exists
	baseTagName := fmt.Sprintf("archive/%s", branchName)
	if _, err := createArchiveTag(ctx, envContainer, baseTagName, branchName, archiveMessage); err != nil {
		return err
	}

	// Delete the branch before removing the worktree
	// Use -D to force delete even if not fully merged
	deleteBranchResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"branch", "-D", branchName},
	})
	if err != nil {
		return fmt.Errorf("failed to execute branch delete command: %v", err)
	}
	if deleteBranchResult.ExitStatus != 0 {
		return fmt.Errorf("failed to delete branch %s: %s", branchName, deleteBranchResult.Stderr)
	}

	// Remove the current worktree using "." since we're running from within the worktree
	// The working directory is the same as the worktree path that needs to be removed
	removeResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"worktree", "remove", "."},
	})
	if err != nil {
		return fmt.Errorf("failed to execute worktree remove command: %v", err)
	}
	if removeResult.ExitStatus != 0 {
		return fmt.Errorf("failed to remove current worktree: %s", removeResult.Stderr)
	}

	return nil
}

// createArchiveTag creates an archive tag, using a suffixed name if the base tag already exists.
// Returns the name of the successfully created tag.
func createArchiveTag(ctx context.Context, envContainer env.EnvContainer, baseTagName, branchName, archiveMessage string) (string, error) {
	// List existing tags matching the pattern
	listResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"tag", "--list", baseTagName + "*"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to list existing tags: %v", err)
	}
	if listResult.ExitStatus != 0 {
		return "", fmt.Errorf("failed to list existing tags: %s", listResult.Stderr)
	}

	tagName := findNextAvailableTagName(baseTagName, listResult.Stdout)

	var tagArgs []string
	if archiveMessage != "" {
		tagArgs = []string{"tag", "-m", archiveMessage, tagName, branchName}
	} else {
		tagArgs = []string{"tag", tagName, branchName}
	}

	tagResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         tagArgs,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute tag creation command: %v", err)
	}

	if tagResult.ExitStatus != 0 {
		return "", fmt.Errorf("failed to create archive tag %s: %s", tagName, tagResult.Stderr)
	}

	return tagName, nil
}

// findNextAvailableTagName determines the next available tag name given existing tags.
func findNextAvailableTagName(baseTagName, existingTagsOutput string) string {
	if existingTagsOutput == "" {
		return baseTagName
	}

	existingTags := make(map[string]bool)
	for _, tag := range strings.Split(strings.TrimSpace(existingTagsOutput), "\n") {
		existingTags[tag] = true
	}

	if !existingTags[baseTagName] {
		return baseTagName
	}

	for i := 2; ; i++ {
		tagName := fmt.Sprintf("%s-%d", baseTagName, i)
		if !existingTags[tagName] {
			return tagName
		}
	}
}

// ListWorktreesActivity lists all Git worktrees for a given repository directory.
// It returns a slice of GitWorktree structs, each containing the absolute,
// symlink-resolved path and the corresponding branch name.
// Worktrees with a detached HEAD are excluded.
// FIXME: adjust to take in an env.EnvContainer instead of repoDir, and use
// EnvRunCommand instead of runGitCommand
func ListWorktreesActivity(ctx context.Context, repoDir string) ([]GitWorktree, error) {
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return nil, err
	}

	commandResult, err := devEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "list", "--porcelain"},
	})
	if err != nil {
		return nil, err
	}
	if commandResult.ExitStatus != 0 {
		return nil, fmt.Errorf("failed to list worktrees: %s", commandResult.Stderr)
	}

	var worktrees []GitWorktree
	entries := strings.Split(strings.TrimSpace(commandResult.Stdout), "\n\n")

	for _, entry := range entries {
		lines := strings.Split(entry, "\n")
		var path, branch string
		isDetached := false

		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				path = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch refs/heads/") {
				branch = strings.TrimPrefix(line, "branch refs/heads/")
			} else if line == "detached" {
				isDetached = true
				// If detached, we don't care about the branch line even if present (unlikely)
				branch = "" // Ensure branch is empty if detached
				break       // No need to parse further lines for branch info if detached
			}
		}

		// Only add if we found a path and a non-detached branch
		if path != "" && branch != "" && !isDetached {
			worktrees = append(worktrees, GitWorktree{Path: path, Branch: branch})
		} else if path != "" && !isDetached && branch == "" {
			// This case might occur for the main worktree if it's somehow detached
			// but the 'detached' line wasn't present, or if parsing failed.
			// Or, more likely, the main worktree before the first commit.
			// Let's check if it's the main worktree path.
			absRepoDir, _ := filepath.Abs(repoDir)
			if path == absRepoDir {
				// It's the main worktree, possibly without a branch yet (pre-commit).
				// Or it could be detached without the 'detached' flag (less common).
				// We need a branch name for the map value. Let's skip it if no branch is found.
				// Alternatively, we could try GetCurrentBranch, but let's keep this focused.
				// For now, skip if no branch ref is explicitly listed.
			}
		}
	}

	return worktrees, nil
}
