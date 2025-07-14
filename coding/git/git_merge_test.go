package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitMergeActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("no worktree, no conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommit(t, repoDir, "Initial commit on main")

		// Create feature branch and add a commit
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		featureCommit := createCommit(t, repoDir, "Feature commit")

		// Merge
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify original branch is restored
		currentBranch := runGitCommandInTestRepo(t, repoDir, "rev-parse", "--abbrev-ref", "HEAD")
		assert.Equal(t, "feature", currentBranch)

		// Verify feature commit is in main's history
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		output := runGitCommandInTestRepo(t, repoDir, "rev-list", "main")
		assert.Contains(t, output, featureCommit)
	})

	t.Run("no worktree, with conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Initial commit on main
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("initial content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Initial commit")

		// Create feature branch from main
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		// Change file on feature branch
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("feature content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Feature commit")

		// Change file on main branch to create conflict
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("main content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Main commit")

		// Go back to feature branch before calling merge
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")

		// Merge
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err, "GitMergeActivity should not return an operational error on merge conflicts")
		assert.True(t, result.HasConflicts)

		// If the merge was aborted correctly, the original branch should be restored.
		// Note: This check will fail if the implementation does not abort the merge on conflict.
		currentBranch := runGitCommandInTestRepo(t, repoDir, "rev-parse", "--abbrev-ref", "HEAD")
		assert.Equal(t, "feature", currentBranch)
	})

	t.Run("with worktree, no conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommit(t, repoDir, "Initial commit")

		// Create and checkout feature branch
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		featureCommit := createCommit(t, repoDir, "Feature commit")

		// Create a worktree for the main branch
		worktreeDir := t.TempDir()
		runGitCommandInTestRepo(t, repoDir, "worktree", "add", worktreeDir, "main")
		defer runGitCommandInTestRepo(t, repoDir, "worktree", "remove", worktreeDir)

		// Merge
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify feature commit is in main's history in the worktree
		output := runGitCommandInTestRepo(t, worktreeDir, "rev-list", "main")
		assert.Contains(t, output, featureCommit)
	})

	t.Run("with worktree, with conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Initial commit on main
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("initial content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Initial commit")

		// Create feature branch from main
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		// Change file on feature branch
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("feature content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Feature commit")

		// Create a worktree for the main branch
		worktreeDir := t.TempDir()
		runGitCommandInTestRepo(t, repoDir, "worktree", "add", worktreeDir, "main")
		defer func() {
			// Abort merge in worktree before removing it, just in case it's in a merge state.
			runGitCommandInTestRepo(t, worktreeDir, "merge", "--abort")
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", worktreeDir)
		}()

		// Change file on main branch in the worktree to create conflict
		err = os.WriteFile(filepath.Join(worktreeDir, "file.txt"), []byte("main content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, worktreeDir, "add", "file.txt")
		createCommit(t, worktreeDir, "Main commit in worktree")

		// Merge
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.True(t, result.HasConflicts)

		// Verify merge state in worktree
		status := runGitCommandInTestRepo(t, worktreeDir, "status")
		assert.Contains(t, status, "Unmerged paths")
	})
}
