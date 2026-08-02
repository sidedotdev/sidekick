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
	t.Parallel()
	ctx := context.Background()

	// spaces in path to exercise our command execution (the built-in
	// SIDE_DATA_HOME on macos has spaces in "Application Support")
	worktreeBaseDir := filepath.Join(t.TempDir(), "dir with spaces")
	err := os.Mkdir(worktreeBaseDir, 0755)
	require.NoError(t, err)

	t.Run("no worktree, no conflicts", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
		// Setup
		repoDir := setupTestGitRepo(t)
		createCommit(t, repoDir, "Initial commit")

		worktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(context.Background(), env.LocalEnvParams{RepoDir: repoDir, WorktreeBaseDir: worktreeBaseDir}, worktree)
		require.NoError(t, err)
		defer func() {
			// Clean up worktree. Sidekick worktrees are created locked, so
			// removal requires --force twice.
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
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
		t.Parallel()
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
		envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
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
		t.Parallel()
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
		envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, featureWorktree)
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
	t.Parallel()
	ctx := context.Background()

	worktreeBaseDir := filepath.Join(t.TempDir(), "data home")
	err := os.Mkdir(worktreeBaseDir, 0755)
	require.NoError(t, err)

	t.Run("squash merge no worktree", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
		// Setup
		repoDir := setupTestGitRepo(t)
		createCommitWithFile(t, repoDir, "Initial commit", "initial.txt", "initial content")

		worktree := domain.Worktree{
			Name:        "feature",
			WorkspaceId: t.Name(),
		}
		envContainer, err := env.NewLocalGitWorktreeActivity(context.Background(), env.LocalEnvParams{RepoDir: repoDir, WorktreeBaseDir: worktreeBaseDir}, worktree)
		require.NoError(t, err)
		defer func() {
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
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
		t.Parallel()
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommitWithFile(t, repoDir, "Initial commit on main", "initial.txt", "initial content")

		// Create feature branch with file change
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "side/feature")
		createCommitWithFile(t, repoDir, "Feature commit", "feature.txt", "feature content")

		// Squash merge without commit message
		params := GitMergeParams{
			SourceBranch:  "side/feature",
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
		assert.Contains(t, logOutput, "Squash merge branch side/feature")
	})

	t.Run("regular merge preserves commits", func(t *testing.T) {
		t.Parallel()
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

	t.Run("squash merge with nothing to commit no worktree", func(t *testing.T) {
		t.Parallel()
		// Setup
		repoDir := setupTestGitRepo(t)
		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}
		createCommitWithFile(t, repoDir, "Initial commit on main", "initial.txt", "initial content")

		// Feature branch adds a file with specific content
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createCommitWithFile(t, repoDir, "Feature commit", "dup.txt", "same content")

		// Main independently adds the identical file, so squashing feature
		// yields no net change to commit
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createCommitWithFile(t, repoDir, "Main commit", "dup.txt", "same content")

		params := GitMergeParams{
			SourceBranch:  "feature",
			TargetBranch:  "main",
			MergeStrategy: MergeStrategySquash,
			CommitMessage: "Squashed feature changes",
		}
		result, err := GitMergeActivity(ctx, envContainer, params)

		// A no-op squash must succeed rather than error on the empty commit
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)
	})

	t.Run("squash merge with nothing to commit in worktree", func(t *testing.T) {
		t.Parallel()
		// Setup
		repoDir := setupTestGitRepo(t)
		createCommitWithFile(t, repoDir, "Initial commit", "initial.txt", "initial content")

		// Target branch lives in a worktree
		worktree := domain.Worktree{
			Name:        "target",
			WorkspaceId: t.Name(),
		}
		targetEnvContainer, err := env.NewLocalGitWorktreeActivity(context.Background(), env.LocalEnvParams{RepoDir: repoDir, WorktreeBaseDir: worktreeBaseDir}, worktree)
		require.NoError(t, err)
		defer func() {
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", targetEnvContainer.Env.GetWorkingDirectory())
		}()

		// Source feature branch adds a file with specific content
		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createCommitWithFile(t, repoDir, "Feature commit", "dup.txt", "same content")
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")

		// Target worktree independently adds the identical file, so squashing
		// feature into it yields no net change to commit
		createCommitWithFile(t, targetEnvContainer.Env.GetWorkingDirectory(), "Target commit", "dup.txt", "same content")

		params := GitMergeParams{
			SourceBranch:  "feature",
			TargetBranch:  worktree.Name,
			MergeStrategy: MergeStrategySquash,
			CommitMessage: "Squashed feature changes",
		}
		result, err := GitMergeActivity(ctx, targetEnvContainer, params)

		// A no-op squash must succeed rather than error on the empty commit
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)
	})
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

func TestGitMergeActivityDirtyTargetWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (repoDir string, envContainer env.EnvContainer) {
		worktreeBaseDir := filepath.Join(t.TempDir(), "dir with spaces")
		require.NoError(t, os.Mkdir(worktreeBaseDir, 0755))

		repoDir = setupTestGitRepo(t)

		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("initial content"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("other initial"), 0644))
		runGitCommandInTestRepo(t, repoDir, "add", ".")
		createCommit(t, repoDir, "Initial commit")

		worktree := domain.Worktree{Name: "feature", WorkspaceId: t.Name()}
		var err error
		envContainer, err = env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
		require.NoError(t, err)
		t.Cleanup(func() {
			runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
		})

		featureDir := envContainer.Env.GetWorkingDirectory()
		require.NoError(t, os.WriteFile(filepath.Join(featureDir, "file.txt"), []byte("feature content"), 0644))
		runGitCommandInTestRepo(t, featureDir, "add", "file.txt")
		createCommit(t, featureDir, "Feature commit")

		return repoDir, envContainer
	}

	t.Run("non-conflicting dirty target is stashed, merged, and restored", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setup(t)

		// Uncommitted local change to a file the merge does not touch.
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("uncommitted local change"), 0644))

		result, err := GitMergeActivity(ctx, envContainer, GitMergeParams{SourceBranch: "feature", TargetBranch: "main"})
		require.NoError(t, err)
		assert.False(t, result.HasConflicts)

		mergedContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "feature content", string(mergedContent))

		restored, err := os.ReadFile(filepath.Join(repoDir, "other.txt"))
		require.NoError(t, err)
		assert.Equal(t, "uncommitted local change", string(restored))
	})

	t.Run("conflicting stash restore is relocated to the source worktree", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setup(t)
		featureDir := envContainer.Env.GetWorkingDirectory()

		// Uncommitted local change to the same file the merge modifies, so
		// restoring the stash on top of the merged content conflicts.
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("local uncommitted edit"), 0644))

		result, err := GitMergeActivity(ctx, envContainer, GitMergeParams{SourceBranch: "feature", TargetBranch: "main"})
		require.NoError(t, err)
		assert.True(t, result.HasConflicts)
		assert.False(t, result.ConflictOnTargetBranch)
		assert.Equal(t, evalSymlinks(t, featureDir), evalSymlinks(t, result.ConflictDirPath))
		assert.Equal(t, evalSymlinks(t, repoDir), evalSymlinks(t, result.BaseStashWorktreePath))
		assert.NotEmpty(t, result.BaseStashSha)

		// The base worktree keeps the clean merge result, with the conflicted
		// stash pop discarded. The original stash entry is intentionally
		// preserved (for recoverability) until the resolved changes are
		// transferred back, so it still references BaseStashSha.
		baseContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "feature content", string(baseContent))
		assert.Equal(t, result.BaseStashSha, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "stash@{0}")))

		// The conflict markers are now present in the source worktree for the
		// resolution agent to edit.
		ownContent, err := os.ReadFile(filepath.Join(featureDir, "file.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(ownContent), "<<<<<<<")
		assert.Contains(t, string(ownContent), "local uncommitted edit")
	})
}

func TestGitTransferWorktreeChangesActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worktreeBaseDir := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.Mkdir(worktreeBaseDir, 0755))

	repoDir := setupTestGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("base content\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", ".")
	createCommit(t, repoDir, "Initial commit")

	worktree := domain.Worktree{Name: "feature", WorkspaceId: t.Name()}
	envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
	require.NoError(t, err)
	t.Cleanup(func() {
		runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
	})
	sourceDir := envContainer.Env.GetWorkingDirectory()

	// Uncommitted resolved changes live in the source worktree.
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved content\n"), 0644))

	err = GitTransferWorktreeChangesActivity(ctx, envContainer, GitTransferWorktreeChangesParams{
		SourceWorktreePath: sourceDir,
		TargetWorktreePath: repoDir,
	})
	require.NoError(t, err)

	// Changes moved to the base worktree as uncommitted changes.
	baseContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved content\n", string(baseContent))

	// Source worktree is left clean and no stash entries linger.
	assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "status", "--porcelain")))
	assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "stash", "list")))
}

func TestGitTransferWorktreeChangesActivityDropsPreservedBaseStash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worktreeBaseDir := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.Mkdir(worktreeBaseDir, 0755))

	repoDir := setupTestGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("base content\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", ".")
	createCommit(t, repoDir, "Initial commit")

	worktree := domain.Worktree{Name: "feature", WorkspaceId: t.Name()}
	envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
	require.NoError(t, err)
	t.Cleanup(func() {
		runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
	})
	sourceDir := envContainer.Env.GetWorkingDirectory()

	// Simulate the preserved base stash kept around for recoverability during
	// resolution: a stash entry on the base worktree that the transfer should
	// drop once the resolved changes are safely moved back.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("preserved base work\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "stash", "push", "-m", "preserved-base")
	baseStashSha := strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "stash@{0}"))

	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved content\n"), 0644))

	err = GitTransferWorktreeChangesActivity(ctx, envContainer, GitTransferWorktreeChangesParams{
		SourceWorktreePath: sourceDir,
		TargetWorktreePath: repoDir,
		BaseStashSha:       baseStashSha,
	})
	require.NoError(t, err)

	baseContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved content\n", string(baseContent))

	// Both the transfer stash and the preserved base stash are dropped.
	assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "stash", "list")))
	assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "stash", "list")))
}

func TestGitTransferWorktreeChangesActivityDropsBaseStashOnNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worktreeBaseDir := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.Mkdir(worktreeBaseDir, 0755))

	repoDir := setupTestGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("base content\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", ".")
	createCommit(t, repoDir, "Initial commit")

	worktree := domain.Worktree{Name: "feature", WorkspaceId: t.Name()}
	envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
	require.NoError(t, err)
	t.Cleanup(func() {
		runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
	})
	sourceDir := envContainer.Env.GetWorkingDirectory()

	// Preserved base stash exists, but the source worktree has no changes to
	// transfer (a successful no-op resolution).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("preserved base work\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "stash", "push", "-m", "preserved-base")
	baseStashSha := strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "rev-parse", "stash@{0}"))

	err = GitTransferWorktreeChangesActivity(ctx, envContainer, GitTransferWorktreeChangesParams{
		SourceWorktreePath: sourceDir,
		TargetWorktreePath: repoDir,
		BaseStashSha:       baseStashSha,
	})
	require.NoError(t, err)

	// The preserved base stash is dropped even though there was nothing to
	// transfer.
	assert.Empty(t, strings.TrimSpace(runGitCommandInTestRepo(t, repoDir, "stash", "list")))

	// The no-op must not mistake the pre-existing (repository-wide) base stash
	// for the transfer stash and apply it onto the target worktree.
	baseContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "base content\n", string(baseContent))
}

func TestGitTransferWorktreeChangesActivityPreservesExistingStash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worktreeBaseDir := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.Mkdir(worktreeBaseDir, 0755))

	repoDir := setupTestGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("base content\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "stashed.txt"), []byte("original\n"), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", ".")
	createCommit(t, repoDir, "Initial commit")

	worktree := domain.Worktree{Name: "feature", WorkspaceId: t.Name()}
	envContainer, err := env.NewLocalGitWorktreeActivity(ctx, env.LocalEnvParams{RepoDir: repoDir, StartBranch: utils.Ptr("main"), WorktreeBaseDir: worktreeBaseDir}, worktree)
	require.NoError(t, err)
	t.Cleanup(func() {
		runGitCommandInTestRepo(t, repoDir, "worktree", "remove", "--force", "--force", envContainer.Env.GetWorkingDirectory())
	})
	sourceDir := envContainer.Env.GetWorkingDirectory()

	// A pre-existing, unrelated stash in the source worktree must not be
	// touched by the transfer (which is namespaced by stash SHA).
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "stashed.txt"), []byte("preexisting work\n"), 0644))
	runGitCommandInTestRepo(t, sourceDir, "stash", "push", "-m", "preexisting")
	existingStashSha := strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "rev-parse", "stash@{0}"))

	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("resolved content\n"), 0644))

	err = GitTransferWorktreeChangesActivity(ctx, envContainer, GitTransferWorktreeChangesParams{
		SourceWorktreePath: sourceDir,
		TargetWorktreePath: repoDir,
	})
	require.NoError(t, err)

	baseContent, err := os.ReadFile(filepath.Join(repoDir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "resolved content\n", string(baseContent))

	// The pre-existing stash entry is still present and unchanged.
	stashList := runGitCommandInTestRepo(t, sourceDir, "stash", "list")
	assert.Contains(t, stashList, "preexisting")
	assert.Equal(t, existingStashSha, strings.TrimSpace(runGitCommandInTestRepo(t, sourceDir, "rev-parse", "stash@{0}")))
}

// targetBranchSyncerEnv wraps a real env to simulate an environment whose
// repository is an independent clone of a host repository, refreshing
// branches from the host repo on demand.
type targetBranchSyncerEnv struct {
	env.Env
	t              *testing.T
	envRepoDir     string
	hostRepoDir    string
	syncedBranches []string
}

func (e *targetBranchSyncerEnv) SyncBranchToRemote(ctx context.Context, branch string) error {
	e.syncedBranches = append(e.syncedBranches, branch)
	runGitCommandInTestRepo(e.t, e.envRepoDir, "fetch", e.hostRepoDir, "+refs/heads/"+branch+":refs/heads/"+branch)
	return nil
}

func TestGitMergeActivityRefreshesTargetBranchFromLocal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	envRepoDir := setupTestGitRepo(t)
	createCommitWithFile(t, envRepoDir, "Initial commit", "base.txt", "base")

	// The host repo's main advances after the env repo's clone point, so the
	// env repo's main is stale when the merge is requested.
	hostRepoDir := filepath.Join(t.TempDir(), "host")
	runGitCommandInTestRepo(t, envRepoDir, "clone", envRepoDir, hostRepoDir)
	createCommitWithFile(t, hostRepoDir, "Host commit", "host.txt", "host")
	hostCommit := runGitCommandInTestRepo(t, hostRepoDir, "rev-parse", "main")

	runGitCommandInTestRepo(t, envRepoDir, "checkout", "-b", "feature")
	createCommitWithFile(t, envRepoDir, "Feature commit", "feature.txt", "feature")
	featureCommit := runGitCommandInTestRepo(t, envRepoDir, "rev-parse", "feature")

	localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: envRepoDir})
	require.NoError(t, err)
	syncerEnv := &targetBranchSyncerEnv{Env: localEnv, t: t, envRepoDir: envRepoDir, hostRepoDir: hostRepoDir}

	result, err := GitMergeActivity(ctx, env.EnvContainer{Env: syncerEnv}, GitMergeParams{
		SourceBranch: "feature",
		TargetBranch: "main",
	})

	require.NoError(t, err)
	assert.False(t, result.HasConflicts)
	assert.Equal(t, []string{"main"}, syncerEnv.syncedBranches)

	// The merge must include the host repo's commits so the result can
	// fast-forward the host branch afterwards.
	mergedCommits := runGitCommandInTestRepo(t, envRepoDir, "rev-list", "main")
	assert.Contains(t, mergedCommits, hostCommit)
	assert.Contains(t, mergedCommits, featureCommit)
}
