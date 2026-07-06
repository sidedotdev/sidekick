package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupHibernationTestRepo creates a git repo with an initial commit and a
// worktree checked out on a side branch, returning the main repo dir, the
// worktree dir, and the worktree's branch name.
func setupHibernationTestRepo(t *testing.T) (repoDir, worktreeDir, branchName string) {
	t.Helper()
	repoDir = t.TempDir()

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}

	runGit(repoDir, "init")
	runGit(repoDir, "config", "user.name", "Test")
	runGit(repoDir, "config", "user.email", "test@test.com")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test"), 0644))
	runGit(repoDir, "add", ".")
	runGit(repoDir, "commit", "-m", "initial commit")

	branchName = "side/test-branch"
	worktreeDir = filepath.Join(t.TempDir(), "worktree")
	runGit(repoDir, "worktree", "add", "-b", branchName, worktreeDir, "HEAD")
	return repoDir, worktreeDir, branchName
}

func runGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
	return string(out)
}

func TestIsHibernated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, IsHibernated(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, HibernationMetadataFile), []byte("{}"), 0644))
	assert.True(t, IsHibernated(dir))
}

func TestHibernateEnv_EmptyBranchName(t *testing.T) {
	t.Parallel()
	_, worktreeDir, _ := setupHibernationTestRepo(t)
	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch name is required")
}

func TestHibernateEnvAndWake_Basic(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "README.md"), []byte("# Modified"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "new_file.txt"), []byte("new content"), 0644))

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	meta, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	assert.Equal(t, branchName, meta.BranchName)
	assert.True(t, IsHibernated(worktreeDir))
	assert.FileExists(t, filepath.Join(worktreeDir, HibernationMetadataFile))
	assert.FileExists(t, filepath.Join(worktreeDir, HibernationPatchFile))

	woken, err := WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)
	assert.Equal(t, branchName, woken.BranchName)
	assert.False(t, IsHibernated(worktreeDir))

	content, err := os.ReadFile(filepath.Join(worktreeDir, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Modified", string(content))
	content, err = os.ReadFile(filepath.Join(worktreeDir, "new_file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
}

// TestHibernateEnvAndWake_LegacyUnlinkedGit covers worktrees hibernated by an
// older build that removed the .git link and pruned the worktree registration.
// Wake must recreate the worktree and restore state via the fresh (legacy) path.
func TestHibernateEnvAndWake_LegacyUnlinkedGit(t *testing.T) {
	t.Parallel()
	repoDir, worktreeDir, branchName := setupHibernationTestRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "README.md"), []byte("# Modified"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "new_file.txt"), []byte("new content"), 0644))

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)

	// Reproduce the legacy on-disk shape: drop the .git link and prune the now
	// dangling worktree registration. (Prune is safe here in an isolated test
	// repo; production hibernate/wake must never prune.)
	require.NoError(t, os.Remove(filepath.Join(worktreeDir, ".git")))
	runGitIn(t, repoDir, "worktree", "prune")

	woken, err := WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)
	assert.Equal(t, branchName, woken.BranchName)
	assert.False(t, IsHibernated(worktreeDir))

	content, err := os.ReadFile(filepath.Join(worktreeDir, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Modified", string(content))
	content, err = os.ReadFile(filepath.Join(worktreeDir, "new_file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
	branch := runGitIn(t, worktreeDir, "rev-parse", "--abbrev-ref", "HEAD")
	assert.Contains(t, branch, branchName)
}

func TestHibernateEnvAndWake_PreservesStagedState(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "staged.txt"), []byte("staged content"), 0644))
	runGitIn(t, worktreeDir, "add", "staged.txt")
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "README.md"), []byte("# Unstaged edit"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "untracked.txt"), []byte("untracked content"), 0644))

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)

	for name, want := range map[string]string{
		"staged.txt":    "staged content",
		"README.md":     "# Unstaged edit",
		"untracked.txt": "untracked content",
	} {
		content, err := os.ReadFile(filepath.Join(worktreeDir, name))
		require.NoError(t, err, name)
		assert.Equal(t, want, string(content), name)
	}

	stagedDiff := runGitIn(t, worktreeDir, "diff", "--cached", "--name-only")
	assert.Contains(t, stagedDiff, "staged.txt")
	assert.NotContains(t, stagedDiff, "README.md")
	branch := runGitIn(t, worktreeDir, "rev-parse", "--abbrev-ref", "HEAD")
	assert.Contains(t, branch, branchName)
}

func TestHibernateEnvAndWake_ExactCommitRestoration(t *testing.T) {
	t.Parallel()
	repoDir, worktreeDir, branchName := setupHibernationTestRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "file1.txt"), []byte("v1"), 0644))
	runGitIn(t, worktreeDir, "add", ".")
	runGitIn(t, worktreeDir, "commit", "-m", "add file1")
	headBefore := strings.TrimSpace(runGitIn(t, worktreeDir, "rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "file1.txt"), []byte("v2-uncommitted"), 0644))

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)

	// Force the branch to diverge from the hibernated HEAD while hibernated. The
	// retained worktree registration now protects the branch, so bypass that via
	// update-ref plumbing to simulate an out-of-band move (e.g. a force push).
	runGitIn(t, repoDir, "update-ref", "refs/heads/"+branchName, "HEAD")

	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)

	headAfter := strings.TrimSpace(runGitIn(t, worktreeDir, "rev-parse", "HEAD"))
	assert.Equal(t, headBefore, headAfter, "HEAD should be at the exact hibernated commit")
	headRef := strings.TrimSpace(runGitIn(t, worktreeDir, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, "HEAD", headRef, "should be detached when branch diverged")
	branchSHA := strings.TrimSpace(runGitIn(t, repoDir, "rev-parse", branchName))
	assert.NotEqual(t, headBefore, branchSHA, "branch ref should not be overwritten by wake")
	content, err := os.ReadFile(filepath.Join(worktreeDir, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "v2-uncommitted", string(content))
}

func TestHibernateEnvAndWake_Deletions(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "to_delete.txt"), []byte("will be deleted"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "to_keep.txt"), []byte("keep me"), 0644))
	runGitIn(t, worktreeDir, "add", ".")
	runGitIn(t, worktreeDir, "commit", "-m", "add files")

	require.NoError(t, os.Remove(filepath.Join(worktreeDir, "to_delete.txt")))
	require.NoError(t, os.Remove(filepath.Join(worktreeDir, "README.md")))
	runGitIn(t, worktreeDir, "add", "README.md")

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(worktreeDir, "to_delete.txt"))
	assert.NoFileExists(t, filepath.Join(worktreeDir, "README.md"))
	content, err := os.ReadFile(filepath.Join(worktreeDir, "to_keep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))

	stagedDiff := runGitIn(t, worktreeDir, "diff", "--cached", "--name-only")
	assert.Contains(t, stagedDiff, "README.md", "staged deletion should be preserved")
	unstagedDiff := runGitIn(t, worktreeDir, "diff", "--name-only")
	assert.Contains(t, unstagedDiff, "to_delete.txt", "unstaged deletion should be preserved")
}

func TestHibernateEnvAndWake_NoChanges(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)
	e := &LocalEnv{WorkingDirectory: worktreeDir}

	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	assert.True(t, IsHibernated(worktreeDir))
	woken, err := WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)
	assert.Equal(t, branchName, woken.BranchName)
	assert.False(t, IsHibernated(worktreeDir))

	// Worktree is still a functional git worktree after waking.
	runGitIn(t, worktreeDir, "status")
}

func TestHibernateEnvAndWake_Idempotency(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)
	e := &LocalEnv{WorkingDirectory: worktreeDir}

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "cycle.txt"), []byte("round one"), 0644))

	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	// Re-hibernating is a no-op that returns the existing metadata.
	meta2, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	assert.Equal(t, branchName, meta2.BranchName)

	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)
	// Waking an already-woken worktree is a no-op.
	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(worktreeDir, "cycle.txt"))
	require.NoError(t, err)
	assert.Equal(t, "round one", string(content))

	runGitIn(t, worktreeDir, "add", ".")
	runGitIn(t, worktreeDir, "commit", "-m", "post-wake commit")
	assert.Contains(t, runGitIn(t, worktreeDir, "log", "--oneline", "-1"), "post-wake commit")
}

// TestHibernateEnvAndWake_TracksBinaryModifications verifies that a staged
// modification to a tracked binary file survives hibernate/wake with both its
// content and its staged state intact, while an untracked binary file is
// dropped (not worth the archive space).
func TestHibernateEnvAndWake_TracksBinaryModifications(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupHibernationTestRepo(t)

	origBin := []byte{0x00, 0x01, 0x02, 0x00, 0xff, 0x00}
	newBin := []byte{0x00, 0x09, 0x08, 0x00, 0x07, 0x06, 0x00}
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "asset.bin"), origBin, 0644))
	runGitIn(t, worktreeDir, "add", "asset.bin")
	runGitIn(t, worktreeDir, "commit", "-m", "add binary asset")

	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "asset.bin"), newBin, 0644))
	runGitIn(t, worktreeDir, "add", "asset.bin")
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "untracked.bin"), []byte{0x00, 0x10, 0x00, 0x20}, 0644))

	e := &LocalEnv{WorkingDirectory: worktreeDir}
	_, err := HibernateEnv(context.Background(), e, branchName)
	require.NoError(t, err)
	_, err = WakeHibernatedEnv(context.Background(), e)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(worktreeDir, "asset.bin"))
	require.NoError(t, err)
	assert.Equal(t, newBin, got, "staged binary modification content should be restored")

	stagedDiff := runGitIn(t, worktreeDir, "diff", "--cached", "--name-only")
	assert.Contains(t, stagedDiff, "asset.bin", "staged binary modification should remain staged")

	assert.NoFileExists(t, filepath.Join(worktreeDir, "untracked.bin"), "untracked binary should be dropped")
}