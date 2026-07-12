package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sidekick/env"
)

// setupConflictingBranches creates a repo where merging "feature" into "main"
// conflicts on conflict.txt, and leaves "main" checked out.
func setupConflictingBranches(t *testing.T, ctx context.Context) (string, env.EnvContainer) {
	t.Helper()
	repoDir := setupTestGitRepo(t)
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	createCommitWithFile(t, repoDir, "Initial commit", "conflict.txt", "base content")
	runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
	createCommitWithFile(t, repoDir, "Feature change", "conflict.txt", "feature content")
	runGitCommandInTestRepo(t, repoDir, "checkout", "main")
	createCommitWithFile(t, repoDir, "Main change", "conflict.txt", "main content")
	return repoDir, env.EnvContainer{Env: devEnv}
}

// runConflictingMerge runs a git merge that is expected to fail with conflicts.
func runConflictingMerge(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"merge"}, args...)...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "expected merge to conflict, got: %s", string(output))
	require.Contains(t, string(output), "CONFLICT")
}

func mergeHeadExists(t *testing.T, repoDir string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "MERGE_HEAD")
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

func TestGitMergeAbortActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("aborts conflicted regular merge", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		runConflictingMerge(t, repoDir, "feature")
		require.True(t, mergeHeadExists(t, repoDir))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		assert.False(t, mergeHeadExists(t, repoDir))
		assert.Empty(t, runGitCommandInTestRepo(t, repoDir, "status", "--porcelain"))
		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "main content", string(content))
	})

	t.Run("aborts conflicted squash merge without MERGE_HEAD", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		runConflictingMerge(t, repoDir, "--squash", "feature")
		// A conflicted squash merge leaves no MERGE_HEAD, so a plain
		// `git merge --abort` cannot clean it up.
		require.False(t, mergeHeadExists(t, repoDir))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		assert.Empty(t, runGitCommandInTestRepo(t, repoDir, "status", "--porcelain"))
		squashMsgPath := runGitCommandInTestRepo(t, repoDir, "rev-parse", "--git-path", "SQUASH_MSG")
		_, statErr := os.Stat(filepath.Join(repoDir, squashMsgPath))
		assert.True(t, os.IsNotExist(statErr), "expected SQUASH_MSG to be removed")
		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "main content", string(content))
	})

	t.Run("no-op preserves uncommitted changes when no merge in progress", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("local edit"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("untracked"), 0644))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "local edit", string(content))
		_, statErr := os.Stat(filepath.Join(repoDir, "untracked.txt"))
		assert.NoError(t, statErr)
	})
}
