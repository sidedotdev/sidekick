package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/domain"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListWorktrees(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("No Worktrees (Only Main)", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		_ = os.RemoveAll(nonExistentDir) // Ensure it doesn't exist

		_, err := ListWorktreesActivity(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		// Error comes from runGitCommand's os.Stat check
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		t.Parallel()
		notRepoDir := t.TempDir()
		_, err := ListWorktreesActivity(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		// Error comes from git command failing inside runGitCommand
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}

func TestCleanupWorktreeActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("Successful Cleanup", func(t *testing.T) {
		t.Parallel()
		// Setup main repository
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit on main")

		// Create a feature branch and worktree with unique name
		branchName := fmt.Sprintf("feature-cleanup-test")

		// Create environment container and the associated worktree
		worktree := domain.Worktree{
			Name:        branchName,
			WorkspaceId: fmt.Sprintf("test-workspace-%s", t.Name()),
		}
		devEnv, err := env.NewLocalGitWorktreeEnv(ctx, env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Verify the worktree and branch exist before cleanup
		worktrees, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)
		require.Len(t, worktrees, 2, "Should have main worktree and feature worktree")

		branches := runGitCommandInTestRepo(t, repoDir, "branch")
		assert.Contains(t, branches, branchName, "Feature branch should exist before cleanup")

		// Perform cleanup from within the worktree
		err = CleanupWorktreeActivity(ctx, envContainer, devEnv.GetWorkingDirectory(), branchName, "Test cleanup with archive message")
		require.NoError(t, err, "Cleanup should succeed")

		// Verify the worktree was removed
		worktreesAfter, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)
		require.Len(t, worktreesAfter, 1, "Should only have main worktree after cleanup")
		assert.Equal(t, "main", worktreesAfter[0].Branch, "Remaining worktree should be main")

		// Verify the branch was deleted
		branchesAfter := runGitCommandInTestRepo(t, repoDir, "branch")
		assert.NotContains(t, branchesAfter, branchName, "Feature branch should be deleted after cleanup")

		// Verify the worktree directory no longer exists
		_, err = os.Stat(devEnv.GetWorkingDirectory())
		assert.True(t, os.IsNotExist(err), "Worktree directory should no longer exist")

		// Verify the archive tag was created
		tagOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "archive/*")
		expectedTag := fmt.Sprintf("archive/%s", branchName)
		assert.Contains(t, tagOutput, expectedTag, "Archive tag should be created")

		// Verify the tag message
		tagMessageOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "-n1", expectedTag)
		assert.Contains(t, tagMessageOutput, "Test cleanup with archive message", "Archive tag should have the correct message")
	})

	t.Run("Missing Branch Name", func(t *testing.T) {
		t.Parallel()
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		err = CleanupWorktreeActivity(ctx, envContainer, repoDir, "", "")
		require.Error(t, err, "Should fail with empty branch name")
		assert.Contains(t, err.Error(), "branch name is required", "Error should mention missing branch name")
	})

	t.Run("Non-existent Branch", func(t *testing.T) {
		t.Parallel()
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		err = CleanupWorktreeActivity(ctx, envContainer, repoDir, "non-existent-branch", "")
		require.Error(t, err, "Should fail with non-existent branch")
		assert.Contains(t, err.Error(), "failed to create archive tag", "Error should mention archive tag creation failure")
	})

	t.Run("Successful Cleanup with Empty Archive Message", func(t *testing.T) {
		t.Parallel()
		// Setup main repository
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit on main")

		// Create a feature branch and worktree with unique name
		branchName := fmt.Sprintf("feature-empty-message-test")

		// Create environment container and the worktree
		worktree := domain.Worktree{
			Name:        branchName,
			WorkspaceId: fmt.Sprintf("test-workspace-%s", t.Name()),
		}
		devEnv, err := env.NewLocalGitWorktreeEnv(ctx, env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Perform cleanup with empty archive message
		err = CleanupWorktreeActivity(ctx, envContainer, devEnv.GetWorkingDirectory(), branchName, "")
		require.NoError(t, err, "Cleanup should succeed with empty archive message")

		// Verify the worktree was removed
		worktreesAfter, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)
		require.Len(t, worktreesAfter, 1, "Should only have main worktree after cleanup")
		assert.Equal(t, "main", worktreesAfter[0].Branch, "Remaining worktree should be main")

		// Verify the branch was deleted
		branchesAfter := runGitCommandInTestRepo(t, repoDir, "branch")
		assert.NotContains(t, branchesAfter, branchName, "Feature branch should be deleted after cleanup")

		// Verify the worktree directory no longer exists
		_, err = os.Stat(devEnv.GetWorkingDirectory())
		assert.True(t, os.IsNotExist(err), "Worktree directory should no longer exist")

		// Verify the archive tag was created
		tagOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "archive/*")
		expectedTag := fmt.Sprintf("archive/%s", branchName)
		assert.Contains(t, tagOutput, expectedTag, "Archive tag should be created even with empty message")

		// Verify the tag exists without a message (should not show message in -n1 output)
		tagMessageOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "-n1", expectedTag)
		assert.Contains(t, tagMessageOutput, expectedTag, "Archive tag should exist")
		// The tag should appear without additional message text when created without -m
		assert.NotContains(t, tagMessageOutput, "Test cleanup", "Tag should not have a message when created with empty archiveMessage")
	})

	t.Run("Tag Fallback When Tag Already Exists", func(t *testing.T) {
		t.Parallel()
		// Setup main repository
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit on main")

		// Create a feature branch and worktree
		branchName := "feature-tag-fallback-test"

		// Pre-create the archive tag to simulate it already existing
		runGitCommandInTestRepo(t, repoDir, "branch", branchName)
		runGitCommandInTestRepo(t, repoDir, "tag", fmt.Sprintf("archive/%s", branchName), branchName)
		// Delete the branch so we can recreate it with the worktree
		runGitCommandInTestRepo(t, repoDir, "branch", "-D", branchName)

		// Create environment container and the worktree
		worktree := domain.Worktree{
			Name:        branchName,
			WorkspaceId: fmt.Sprintf("test-workspace-%s", t.Name()),
		}
		devEnv, err := env.NewLocalGitWorktreeEnv(ctx, env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Perform cleanup - should succeed with fallback tag name
		err = CleanupWorktreeActivity(ctx, envContainer, devEnv.GetWorkingDirectory(), branchName, "Fallback test")
		require.NoError(t, err, "Cleanup should succeed with fallback tag name")

		// Verify both tags exist (original and fallback)
		tagOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "archive/*")
		expectedOriginalTag := fmt.Sprintf("archive/%s", branchName)
		expectedFallbackTag := fmt.Sprintf("archive/%s-2", branchName)
		assert.Contains(t, tagOutput, expectedOriginalTag, "Original archive tag should still exist")
		assert.Contains(t, tagOutput, expectedFallbackTag, "Fallback archive tag should be created")

		// Verify the worktree was removed
		worktreesAfter, err := ListWorktreesActivity(ctx, repoDir)
		require.NoError(t, err)
		require.Len(t, worktreesAfter, 1, "Should only have main worktree after cleanup")
	})

	t.Run("Tag Fallback Multiple Existing Tags", func(t *testing.T) {
		t.Parallel()
		// Setup main repository
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit on main")

		branchName := "feature-multi-tag-test"

		// Pre-create multiple archive tags
		runGitCommandInTestRepo(t, repoDir, "branch", branchName)
		runGitCommandInTestRepo(t, repoDir, "tag", fmt.Sprintf("archive/%s", branchName), branchName)
		runGitCommandInTestRepo(t, repoDir, "tag", fmt.Sprintf("archive/%s-2", branchName), branchName)
		runGitCommandInTestRepo(t, repoDir, "tag", fmt.Sprintf("archive/%s-3", branchName), branchName)
		runGitCommandInTestRepo(t, repoDir, "branch", "-D", branchName)

		// Create environment container and the worktree
		worktree := domain.Worktree{
			Name:        branchName,
			WorkspaceId: fmt.Sprintf("test-workspace-%s", t.Name()),
		}
		devEnv, err := env.NewLocalGitWorktreeEnv(ctx, env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Perform cleanup - should succeed with next available fallback tag name
		err = CleanupWorktreeActivity(ctx, envContainer, devEnv.GetWorkingDirectory(), branchName, "")
		require.NoError(t, err, "Cleanup should succeed with next available fallback tag name")

		// Verify the new tag was created with suffix -4
		tagOutput := runGitCommandInTestRepo(t, repoDir, "tag", "-l", "archive/*")
		expectedFallbackTag := fmt.Sprintf("archive/%s-4", branchName)
		assert.Contains(t, tagOutput, expectedFallbackTag, "Should create tag with next available suffix")
	})
}
