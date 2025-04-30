package git

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/env"
	"strings"
)

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
