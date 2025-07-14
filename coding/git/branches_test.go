package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCurrentBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("On Branch", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")
		runGitCommandInTestRepo(t, repoDir, "branch", "develop")
		runGitCommandInTestRepo(t, repoDir, "checkout", "develop")

		state, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.False(t, state.IsDetached, "Should not be in detached HEAD state")
		assert.Equal(t, "develop", state.Name, "Branch name should be 'develop'")
	})

	t.Run("Detached HEAD", func(t *testing.T) {
		repoDir := setupTestGitRepo(t)
		commitHash := createCommit(t, repoDir, "Initial commit")
		runGitCommandInTestRepo(t, repoDir, "checkout", commitHash) // Detach HEAD by checking out commit hash

		state, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.True(t, state.IsDetached, "Should be in detached HEAD state")
		assert.Empty(t, state.Name, "Branch name should be empty in detached HEAD state")
	})

	t.Run("Empty Repository (Initialized)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Sets up repo with 'main' but no commits

		// `git symbolic-ref --short HEAD` should return the initial branch name even before the first commit.
		state, err := GetCurrentBranch(ctx, repoDir)
		require.NoError(t, err)
		assert.False(t, state.IsDetached, "Should not be detached in an empty repo")
		assert.Equal(t, "main", state.Name, "Branch name should be the initial branch 'main'")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		// Use a path that definitely doesn't exist.
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		// Ensure the directory does not exist before the call
		_ = os.RemoveAll(nonExistentDir)

		state, err := GetCurrentBranch(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		// Check if the error indicates the directory issue, as handled by runGitCommand
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate directory not found")
		assert.Empty(t, state.Name, "Branch name should be empty on error")
		assert.False(t, state.IsDetached, "Should not indicate detached state on error")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		// Create a directory but don't initialize git in it.
		notRepoDir := t.TempDir()
		state, err := GetCurrentBranch(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		// Git commands usually include "not a git repository" in stderr
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
		assert.Empty(t, state.Name, "Branch name should be empty on error")
		assert.False(t, state.IsDetached, "Should not indicate detached state on error")
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
	})

	t.Run("Empty Repository (No Commits)", func(t *testing.T) {
		repoDir := setupTestGitRepo(t) // Initializes with main, but no commits

		// `git rev-parse --verify main` fails if no commits exist on the branch.
		_, err := GetDefaultBranch(ctx, repoDir)
		require.Error(t, err, "Should return error in an empty repo")
		// Check the high-level error, but don't assert the exact git stderr message which can vary.
		assert.Contains(t, err.Error(), "could not determine default branch", "Error message should indicate default branch couldn't be found")
	})

	t.Run("Invalid Directory", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent-dir")
		_ = os.RemoveAll(nonExistentDir)

		_, err := GetDefaultBranch(ctx, nonExistentDir)
		require.Error(t, err, "Should return an error for a non-existent directory")
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notGitDir := t.TempDir()
		_, err := GetDefaultBranch(ctx, notGitDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		//assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
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
		assert.Contains(t, err.Error(), "no such file or directory", "Error message should indicate directory not found")
	})

	t.Run("Not a Git Repository", func(t *testing.T) {
		notRepoDir := t.TempDir()
		_, err := ListLocalBranches(ctx, notRepoDir)
		require.Error(t, err, "Should return an error for a directory that is not a git repository")
		assert.Contains(t, err.Error(), "not a git repository", "Error message should indicate it's not a git repository")
	})
}
