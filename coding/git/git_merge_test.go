package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitMergeActivity(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory to act as SIDE_DATA_HOME
	tempHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", tempHome) // Set env var for the test duration

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
		assert.Empty(t, result.ConflictDirPath)
		assert.False(t, result.ConflictOnTargetBranch)

		// Verify feature commit is in main's history
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

	t.Run("source is worktree, with target checked out on repoDir, no conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")

		worktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(context.Background(), env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		defer func() {
			// Clean up worktree
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", envContainer.Env.GetWorkingDirectory())
		}()

		featureCommit := createCommit(t, envContainer.Env.GetWorkingDirectory(), "Feature commit")

		// Merge
		params := GitMergeParams{
			SourceBranch: worktree.Name,
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)
		assert.Empty(t, result.ConflictDirPath)
		assert.False(t, result.ConflictOnTargetBranch)

		// Verify feature commit is in main's history in the worktree
		output := runGitCommandInTestRepo(t, repoDir, "rev-list", "main")
		assert.Contains(t, output, featureCommit)
	})

	t.Run("source is worktree, with target checked out on repoDir, with conflicts", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)

		// Initial commit on main
		err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("initial content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Initial commit")

		// Create worktree for main branch
		worktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main")}, worktree)
		require.NoError(t, err)

		// Change file on feature branch
		err = os.WriteFile(filepath.Join(envContainer.Env.GetWorkingDirectory(), "file.txt"), []byte("feature content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, envContainer.Env.GetWorkingDirectory(), "add", "file.txt")
		createCommit(t, envContainer.Env.GetWorkingDirectory(), "Feature commit")

		// create conflicting change on main
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("main content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Main commit")

		// Merge
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.True(t, result.HasConflicts)

		// Resolve symlinks for path comparison (macOS /var -> /private/var)
		expectedPath, _ := filepath.EvalSymlinks(repoDir)
		actualPath, _ := filepath.EvalSymlinks(result.ConflictDirPath)
		assert.Equal(t, expectedPath, actualPath)
		assert.True(t, result.ConflictOnTargetBranch)

		// Verify merge state in target repoDir
		status := runGitCommandInTestRepo(t, repoDir, "status")
		assert.Contains(t, status, "Unmerged paths")
	})

	t.Run("source is worktree, with target NOT checked out anywhere, with conflicts - reverse merge strategy", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)

		// Initial commit on main
		err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("initial content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Initial commit")

		// Create worktree for feature branch (source branch)
		featureWorktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main")}, featureWorktree)
		require.NoError(t, err)

		// Change file on feature branch
		sourceWorktreePath := envContainer.Env.GetWorkingDirectory()
		err = os.WriteFile(filepath.Join(sourceWorktreePath, "file.txt"), []byte("feature content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, sourceWorktreePath, "add", "file.txt")
		createCommit(t, sourceWorktreePath, "Feature commit")

		// create conflicting change on main
		err = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("main content"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "file.txt")
		createCommit(t, repoDir, "Main commit")

		// go to unused feature branch from main - just don't want main checked
		// out anywhere to trigger this scenario
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "unused_feature")

		// Merge (should trigger reverse merge strategy)
		params := GitMergeParams{
			SourceBranch: "feature",
			TargetBranch: "main",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.True(t, result.HasConflicts)

		// Resolve symlinks for path comparison (macOS /var -> /private/var)
		expectedPath, _ := filepath.EvalSymlinks(envContainer.Env.GetWorkingDirectory())
		actualPath, _ := filepath.EvalSymlinks(result.ConflictDirPath)
		assert.Equal(t, expectedPath, actualPath)
		assert.False(t, result.ConflictOnTargetBranch)

		// Verify merge state in feature worktree (reverse merge conflicts)
		status := runGitCommandInTestRepo(t, envContainer.Env.GetWorkingDirectory(), "status")
		assert.Contains(t, status, "Unmerged paths")

		// Verify original branch remains unchanged
		currentBranch := runGitCommandInTestRepo(t, repoDir, "rev-parse", "--abbrev-ref", "HEAD")
		assert.Equal(t, "unused_feature", currentBranch)
	})
}
