package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListWorktrees(t *testing.T) {
	ctx := context.Background()

	t.Run("No Worktrees (Only Main)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")

		worktrees, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)

		absRepoDir, err := filepath.Abs(repoDir)
		require.NoError(t, err)
		resolvedRepoDir, err := filepath.EvalSymlinks(absRepoDir)
		require.NoError(t, err) // EvalSymlinks should succeed for the temp dir

		expected := []GitWorktree{
			{Path: resolvedRepoDir, Branch: "main"}, // Use resolved path
		}
		assert.Equal(t, expected, worktrees)
	})

	t.Run("Multiple Worktrees", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // main branch
		createCommit(t, repoDir, "Commit 1 on main")

		// Create feature-a branch and worktree
		runGitCommandInTestRepo(t, repoDir, "branch", "feature-a")
		wtADirRelative := "../worktree-a"
		wtADir := filepath.Join(filepath.Dir(repoDir), "worktree-a") // Path outside main repo dir
		_ = os.RemoveAll(wtADir)                                     // Clean up potential leftovers
		runGitCommandInTestRepo(t, repoDir, "worktree", "add", wtADirRelative, "feature-a")
		createCommit(t, wtADir, "Commit 2 on feature-a") // Commit in the worktree

		// Create feature-b branch and worktree
		runGitCommandInTestRepo(t, repoDir, "branch", "feature-b")
		wtBDirRelative := "../worktree-b"
		wtBDir := filepath.Join(filepath.Dir(repoDir), "worktree-b")
		_ = os.RemoveAll(wtBDir)
		runGitCommandInTestRepo(t, repoDir, "worktree", "add", wtBDirRelative, "feature-b")
		createCommit(t, wtBDir, "Commit 3 on feature-b")

		// List worktrees
		worktrees, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)

		// Get absolute, resolved paths for assertion keys
		absRepoDir, err := filepath.Abs(repoDir)
		require.NoError(t, err)
		resolvedRepoDir, err := filepath.EvalSymlinks(absRepoDir)
		require.NoError(t, err)

		absWtADir, err := filepath.Abs(wtADir)
		require.NoError(t, err)
		resolvedWtADir, err := filepath.EvalSymlinks(absWtADir)
		require.NoError(t, err)

		absWtBDir, err := filepath.Abs(wtBDir)
		require.NoError(t, err)
		resolvedWtBDir, err := filepath.EvalSymlinks(absWtBDir)
		require.NoError(t, err)

		// Note: The order might not be guaranteed by git, but usually main is first.
		// testify/assert.ElementsMatch might be better if order is unstable,
		// but assert.Equal often works for slices if elements are comparable.
		expected := []GitWorktree{
			{Path: resolvedRepoDir, Branch: "main"},     // Use resolved path
			{Path: resolvedWtADir, Branch: "feature-a"}, // Use resolved path
			{Path: resolvedWtBDir, Branch: "feature-b"}, // Use resolved path
		}
		// Use ElementsMatch for order-insensitive comparison
		assert.ElementsMatch(t, expected, worktrees)

		// Clean up worktree directories manually (TempDir only cleans repoDir)
		assert.NoError(t, os.RemoveAll(wtADir))
		assert.NoError(t, os.RemoveAll(wtBDir))
	})

	t.Run("Worktree with Detached HEAD", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // main branch
		hash1 := createCommit(t, repoDir, "Commit 1 on main")
		createCommit(t, repoDir, "Commit 2 on main") // HEAD is now commit 2

		// Create detached worktree pointing to commit 1
		wtDetachedDirRelative := "../worktree-detached"
		wtDetachedDir := filepath.Join(filepath.Dir(repoDir), "worktree-detached")
		_ = os.RemoveAll(wtDetachedDir)
		runGitCommandInTestRepo(t, repoDir, "worktree", "add", "--detach", wtDetachedDirRelative, hash1)

		// List worktrees
		worktrees, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)

		// Get absolute, resolved path for assertion key
		absRepoDir, err := filepath.Abs(repoDir)
		require.NoError(t, err)
		resolvedRepoDir, err := filepath.EvalSymlinks(absRepoDir)
		require.NoError(t, err)
		// absWtDetachedDir, err := filepath.Abs(wtDetachedDir) // We don't expect this one
		// require.NoError(t, err)

		// Expect only the main worktree (resolved path), as detached ones are excluded
		expected := []GitWorktree{
			{Path: resolvedRepoDir, Branch: "main"}, // Use resolved path
		}
		assert.Equal(t, expected, worktrees)

		// Clean up worktree directory manually
		assert.NoError(t, os.RemoveAll(wtDetachedDir))
	})

	t.Run("Empty Repository (No Commits)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main, but no commits

		worktrees, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)

		// Before the first commit, `git worktree list --porcelain` *does* list the
		// main worktree and its initial branch (e.g., "main").
		// The function should return this.
		absRepoDir, err := filepath.Abs(repoDir)
		require.NoError(t, err)
		resolvedRepoDir, err := filepath.EvalSymlinks(absRepoDir)
		require.NoError(t, err)

		expected := []GitWorktree{
			{Path: resolvedRepoDir, Branch: "main"}, // Expect the main worktree and its initial branch
		}
		assert.Equal(t, expected, worktrees, "Should return the main worktree even before commits")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		_ = os.RemoveAll(nonExistentDir) // Ensure it doesn't exist

		_, err := ListWorktreesActivity(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		// Error comes from runGitCommand's os.Stat check
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notRepoDir := t.TempDir()
		_, err := ListWorktreesActivity(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		// Error comes from git command failing inside runGitCommand
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}