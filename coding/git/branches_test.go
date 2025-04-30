package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestGitRepo initializes a git repository in a temporary directory.
// It configures a default user and initializes with 'main' as the default branch.
func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()

	// Check if git is installed
	_, err := exec.LookPath("git")
	require.NoError(t, err, "git command not found in PATH")

	// git init with main branch
	cmdInit := exec.Command("git", "init", "-b", "main")
	cmdInit.Dir = repoDir
	outputInit, err := cmdInit.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(outputInit))

	// Configure user name and email for commits
	runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
	runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")

	return repoDir
}

// runGitCommandInTestRepo is a helper to run git commands in the test repo directory.
// It uses require.NoError to fail the test immediately if a command fails.
func runGitCommandInTestRepo(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	// Trim space from output for easier comparison, but include full output in error
	trimmedOutput := strings.TrimSpace(string(output))
	require.NoError(t, err, "git command %v failed in %s:\n%s", args, repoDir, string(output))
	return trimmedOutput
}

// createCommit creates an empty commit with a given message.
func createCommit(t *testing.T, repoDir, message string) string {
	t.Helper()
	runGitCommandInTestRepo(t, repoDir, "commit", "--allow-empty", "-m", message)
	// Get the commit hash
	hash := runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")
	return hash
}

func TestGetCurrentBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("On Branch", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")
		runGitCommandInTestRepo(t, repoDir, "branch", "develop")
		runGitCommandInTestRepo(t, repoDir, "checkout", "develop")

		branch, isDetached, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.False(t, isDetached, "Should not be in detached HEAD state")
		assert.Equal(t, "develop", branch, "Branch name should be 'develop'")
	})

	t.Run("Detached HEAD", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		commitHash := createCommit(t, repoDir, "Initial commit")
		runGitCommandInTestRepo(t, repoDir, "checkout", commitHash) // Detach HEAD by checking out commit hash

		branch, isDetached, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.True(t, isDetached, "Should be in detached HEAD state")
		assert.Empty(t, branch, "Branch name should be empty in detached HEAD state")
	})

	t.Run("Empty Repository (Initialized)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Sets up repo with 'main' but no commits

		// `git symbolic-ref --short HEAD` should return the initial branch name even before the first commit.
		branch, isDetached, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.False(t, isDetached, "Should not be detached in an empty repo")
		assert.Equal(t, "main", branch, "Branch name should be the initial branch 'main'")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		// Use a path that definitely doesn't exist.
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		// Ensure the directory does not exist before the call
		_ = os.RemoveAll(nonExistentDir)

		_, _, err := GetCurrentBranch(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		// Check if the error indicates the directory issue, as handled by runGitCommand
		assert.Contains(t, err.Error(), "repository directory", "Error message should mention the directory")
		assert.Contains(t, err.Error(), "not found", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		// Create a directory but don't initialize git in it.
		notRepoDir := t.TempDir()
		_, _, err := GetCurrentBranch(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		// Git commands usually include "not a git repository" in stderr
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}

func TestGetDefaultBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("Main Exists and Verifiable", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		createCommit(t, repoDir, "Initial commit on main")

		branch, err := GetDefaultBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.Equal(t, "main", branch)
	})

	t.Run("Master Exists and Verifiable (Main does not)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		createCommit(t, repoDir, "Initial commit")
		// Rename main to master
		runGitCommandInTestRepo(t, repoDir, "branch", "-M", "main", "master")

		branch, err := GetDefaultBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("Both Main and Master Exist (Main preferred)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		createCommit(t, repoDir, "Commit on main")
		// Create master branch and add a commit so it's verifiable
		runGitCommandInTestRepo(t, repoDir, "branch", "master")
		runGitCommandInTestRepo(t, repoDir, "checkout", "master")
		createCommit(t, repoDir, "Commit on master")
		runGitCommandInTestRepo(t, repoDir, "checkout", "main") // Switch back

		// Should prefer main
		branch, err := GetDefaultBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.Equal(t, "main", branch)
	})

	t.Run("Neither Main nor Master Exists (Verifiable)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		createCommit(t, repoDir, "Initial commit")
		// Rename main to something else
		runGitCommandInTestRepo(t, repoDir, "branch", "-M", "main", "develop")

		_, err := GetDefaultBranch(ctx, repoDir)
		require.Error(t, err, "Should return error when neither main nor master exists")
		assert.Contains(t, err.Error(), "could not determine default branch")
		assert.Contains(t, err.Error(), "main check failed")   // Should show failure for main
		assert.Contains(t, err.Error(), "master check failed") // Should show failure for master
	})

	t.Run("Empty Repository (No Commits)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main, but no commits

		// `git rev-parse --verify main` fails if no commits exist on the branch.
		_, err := GetDefaultBranch(ctx, repoDir)
		require.Error(t, err, "Should return error in an empty repo")
		// Check the high-level error, but don't assert the exact git stderr message which can vary.
		assert.Contains(t, err.Error(), "could not determine default branch", "Error message should indicate default branch couldn't be found")
		assert.Contains(t, err.Error(), "main check failed", "Error message should indicate 'main' check failed")
		assert.Contains(t, err.Error(), "master check failed", "Error message should indicate 'master' check failed")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		_ = os.RemoveAll(nonExistentDir)

		_, err := GetDefaultBranch(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		assert.Contains(t, err.Error(), "repository directory", "Error message should mention the directory")
		assert.Contains(t, err.Error(), "not found", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notRepoDir := t.TempDir()
		_, err := GetDefaultBranch(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}

func TestListLocalBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("Single Branch", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		createCommit(t, repoDir, "Initial commit")

		branches, err := ListLocalBranches(ctx, repoDir)
		require.NoError(t, err)
		assert.Equal(t, []string{"main"}, branches)
	})

	t.Run("Multiple Branches Sorted by Committer Date", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main
		// Commit times need to be distinct for sorting verification.
		// Use a longer sleep duration to increase reliability on slower/busier CIs.
		const sleepDuration = 1100 * time.Millisecond // Slightly over 1 second

		createCommit(t, repoDir, "Commit 1 on main") // ~T0
		time.Sleep(sleepDuration)                    // Ensure commit times differ reliably

		runGitCommandInTestRepo(t, repoDir, "branch", "feature-a")
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature-a")
		createCommit(t, repoDir, "Commit 2 on feature-a") // ~T1
		time.Sleep(sleepDuration)

		runGitCommandInTestRepo(t, repoDir, "branch", "develop")
		runGitCommandInTestRepo(t, repoDir, "checkout", "develop")
		createCommit(t, repoDir, "Commit 3 on develop") // ~T2
		time.Sleep(sleepDuration)

		// Update main again to make it the newest
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createCommit(t, repoDir, "Commit 4 on main") // ~T3 (Newest)

		branches, err := ListLocalBranches(ctx, repoDir)
		require.NoError(t, err)
		// Expect sort by last commit date (newest first): main (T3), develop (T2), feature-a (T1)
		assert.Equal(t, []string{"main", "develop", "feature-a"}, branches, "Branches should be sorted by newest commit date")
	})

	t.Run("Empty Repository (No Commits)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main, but no commits

		branches, err := ListLocalBranches(ctx, repoDir)
		require.NoError(t, err)
		// `git branch --list` returns empty output and exit code 0 if no commits
		assert.Empty(t, branches, "Should return an empty slice for an empty repository")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		_ = os.RemoveAll(nonExistentDir)

		_, err := ListLocalBranches(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		assert.Contains(t, err.Error(), "repository directory", "Error message should mention the directory")
		assert.Contains(t, err.Error(), "not found", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notRepoDir := t.TempDir()
		_, err := ListLocalBranches(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}

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
		assert.Contains(t, err.Error(), "repository directory", "Error message should mention the directory")
		assert.Contains(t, err.Error(), "not found", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notRepoDir := t.TempDir()
		_, err := ListWorktreesActivity(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		// Error comes from git command failing inside runGitCommand
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}
