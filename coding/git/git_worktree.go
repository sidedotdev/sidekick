package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"

	"github.com/rs/zerolog/log"
)

// removeBlockingUntrackedBinaries deletes untracked binary files from the
// working tree so they don't block a non-forced `git worktree remove`. It only
// acts when untracked binaries are the sole blocking changes; if any tracked
// changes or non-binary untracked files are present, the working tree is left
// untouched so genuine changes continue to block cleanup.
func removeBlockingUntrackedBinaries(ctx context.Context, envContainer env.EnvContainer) error {
	untracked, err := listUntrackedFiles(ctx, envContainer, nil)
	if err != nil {
		return err
	}
	nonBinary, binary, err := partitionUntrackedBinaries(ctx, envContainer, untracked)
	if err != nil {
		return err
	}
	if len(binary) == 0 || len(nonBinary) > 0 {
		return nil
	}

	hasTrackedChanges, err := hasUncommittedTrackedChanges(ctx, envContainer)
	if err != nil {
		return err
	}
	if hasTrackedChanges {
		return nil
	}

	rmOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "rm",
		Args:               append([]string{"-f", "--"}, binary...),
	})
	if err != nil {
		return fmt.Errorf("failed to remove untracked binary files: %w", err)
	}
	if rmOutput.ExitStatus != 0 {
		return fmt.Errorf("failed to remove untracked binary files: %s", rmOutput.Stderr)
	}

	log.Info().Strs("files", binary).Msg("removed untracked binary files to allow worktree cleanup")
	return nil
}

// hasUncommittedTrackedChanges reports whether there are any staged or unstaged
// changes to tracked files (untracked files are ignored).
func hasUncommittedTrackedChanges(ctx context.Context, envContainer env.EnvContainer) (bool, error) {
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"status", "--porcelain", "--untracked-files=no"},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check for tracked changes: %w", err)
	}
	if output.ExitStatus != 0 {
		return false, fmt.Errorf("git status failed with exit status %d: %s", output.ExitStatus, output.Stderr)
	}
	return strings.TrimSpace(output.Stdout) != "", nil
}

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

	// Untracked binaries are usually build artifacts but would otherwise block
	// the non-forced worktree removal below. Remove them so cleanup can proceed,
	// but only when they are the sole blocking changes so that genuine
	// uncommitted/untracked changes still block cleanup.
	if err := removeBlockingUntrackedBinaries(ctx, envContainer); err != nil {
		return err
	}

	// Sidekick worktrees are created locked (to survive "git worktree prune"),
	// so they must be unlocked before removal. Tolerate a "not locked" failure
	// for worktrees created before locking was introduced.
	unlockResult, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "git",
		Args:         []string{"worktree", "unlock", "."},
	})
	if err != nil {
		return fmt.Errorf("failed to execute worktree unlock command: %v", err)
	}
	if unlockResult.ExitStatus != 0 && !strings.Contains(unlockResult.Stderr, "not locked") {
		return fmt.Errorf("failed to unlock current worktree: %s", unlockResult.Stderr)
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

// ListWorktrees lists all Git worktrees for the repository in the given
// environment. It returns a slice of GitWorktree structs, each containing the
// absolute, symlink-resolved path and the corresponding branch name. Worktrees
// with a detached HEAD are excluded.
//
// Running the git command inside the provided environment (local, devpod,
// openshell, etc.) ensures container-relative paths resolve correctly rather
// than being interpreted against the host filesystem.
func ListWorktrees(ctx context.Context, envContainer env.EnvContainer) ([]GitWorktree, error) {
	commandResult, err := envContainer.Env.RunCommand(ctx, env.EnvRunCommandInput{
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

		// Only add if we found a path and a non-detached branch. A worktree
		// without a branch ref (e.g. the main worktree before its first commit)
		// is intentionally skipped since callers key on branch names.
		if path != "" && branch != "" && !isDetached {
			worktrees = append(worktrees, GitWorktree{Path: path, Branch: branch})
		}
	}

	return worktrees, nil
}
