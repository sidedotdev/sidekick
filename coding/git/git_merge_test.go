package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitMergeActivity(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory to act as SIDE_DATA_HOME
	tempDir := t.TempDir()
	// spaces in path to exercise our command execution (the built-in
	// SIDE_DATA_HOME on macos has spaces in "Application Support")
	tempDataHome := filepath.Join(tempDir, "dir with spaces")
	err := os.Mkdir(tempDataHome, 0755)
	require.NoError(t, err)
	// TODO make the tests parallel by setting not needed to set env. this can
	// be done by cloning LocalEnvParams into LocalGitWorktreeEnvParams and
	// adding an optional worktree path there, which the tests will make use of,
	// with separate directories for each.
	t.Setenv("SIDE_DATA_HOME", tempDataHome) // Set env var for the test duration

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

// createCommitWithFile creates a commit with an actual file change
func createCommitWithFile(t *testing.T, repoDir, message, filename, content string) {
	t.Helper()
	filePath := filepath.Join(repoDir, filename)
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)
	runGitCommandInTestRepo(t, repoDir, "add", filename)
	runGitCommandInTestRepo(t, repoDir, "commit", "-m", message)
}

func TestGitMergeActivitySquash(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory to act as SIDE_DATA_HOME
	tempDir := t.TempDir()
	tempDataHome := filepath.Join(tempDir, "data home")
	err := os.Mkdir(tempDataHome, 0755)
	require.NoError(t, err)
	t.Setenv("SIDE_DATA_HOME", tempDataHome)

	t.Run("squash merge no worktree", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommitWithFile(t, repoDir, "Initial commit on main", "initial.txt", "initial content")

		// Create feature branch and add multiple commits with file changes
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createCommitWithFile(t, repoDir, "Feature commit 1", "feature1.txt", "feature 1 content")
		createCommitWithFile(t, repoDir, "Feature commit 2", "feature2.txt", "feature 2 content")

		// Squash merge
		params := GitMergeParams{
			SourceBranch:  "feature",
			TargetBranch:  "main",
			MergeStrategy: MergeStrategySquash,
			CommitMessage: "Squashed feature changes",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify we're on main and there's a single squash commit
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		logOutput := runGitCommandInTestRepo(t, repoDir, "log", "--oneline")
		// Should have 2 commits: initial + squash commit
		lines := strings.Split(strings.TrimSpace(logOutput), "\n")
		assert.Equal(t, 2, len(lines), "Expected 2 commits (initial + squash), got: %s", logOutput)
		assert.Contains(t, logOutput, "Squashed feature changes")
	})

	t.Run("squash merge with worktree", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		createCommitWithFile(t, repoDir, "Initial commit", "initial.txt", "initial content")

		worktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(context.Background(), env.LocalEnvParams{RepoDir: repoDir}, worktree)
		require.NoError(t, err)
		defer func() {
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", envContainer.Env.GetWorkingDirectory())
		}()

		// Add multiple commits with file changes to feature branch
		createCommitWithFile(t, envContainer.Env.GetWorkingDirectory(), "Feature commit 1", "feature1.txt", "feature 1 content")
		createCommitWithFile(t, envContainer.Env.GetWorkingDirectory(), "Feature commit 2", "feature2.txt", "feature 2 content")

		// Squash merge
		params := GitMergeParams{
			SourceBranch:  worktree.Name,
			TargetBranch:  "main",
			MergeStrategy: MergeStrategySquash,
			CommitMessage: "Squashed worktree changes",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify main has squash commit
		logOutput := runGitCommandInTestRepo(t, repoDir, "log", "--oneline", "main")
		lines := strings.Split(strings.TrimSpace(logOutput), "\n")
		assert.Equal(t, 2, len(lines), "Expected 2 commits (initial + squash), got: %s", logOutput)
		assert.Contains(t, logOutput, "Squashed worktree changes")
	})

	t.Run("squash merge default commit message", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommitWithFile(t, repoDir, "Initial commit on main", "initial.txt", "initial content")

		// Create feature branch with file change
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createCommitWithFile(t, repoDir, "Feature commit", "feature.txt", "feature content")

		// Squash merge without commit message
		params := GitMergeParams{
			SourceBranch:  "feature",
			TargetBranch:  "main",
			MergeStrategy: MergeStrategySquash,
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify default commit message
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		logOutput := runGitCommandInTestRepo(t, repoDir, "log", "--oneline")
		assert.Contains(t, logOutput, "Squash merge branch 'feature'")
	})

	t.Run("regular merge preserves commits", func(t *testing.T) {
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommitWithFile(t, repoDir, "Initial commit on main", "initial.txt", "initial content")

		// Create feature branch and add multiple commits with file changes
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createCommitWithFile(t, repoDir, "Feature commit 1", "feature1.txt", "feature 1 content")
		createCommitWithFile(t, repoDir, "Feature commit 2", "feature2.txt", "feature 2 content")

		// Regular merge (not squash)
		params := GitMergeParams{
			SourceBranch:  "feature",
			TargetBranch:  "main",
			MergeStrategy: MergeStrategyMerge,
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// Assertions
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		// Verify all commits are preserved
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		logOutput := runGitCommandInTestRepo(t, repoDir, "log", "--oneline")
		assert.Contains(t, logOutput, "Feature commit 1")
		assert.Contains(t, logOutput, "Feature commit 2")
	})
}
