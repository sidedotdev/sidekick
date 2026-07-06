package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func setupTestRepo(t *testing.T) (repoDir string, worktreeDir string, branchName string) {
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

// TestHibernateWorktreeActivities is a smoke test that the activity wrappers
// delegate to the env-level hibernation logic. The hibernate/wake behavior
// itself (staged state, deletions, exact-commit restoration, idempotency, etc.)
// is covered in depth by the sidekick/env package tests.
func TestHibernateWorktreeActivities(t *testing.T) {
	t.Parallel()
	_, worktreeDir, branchName := setupTestRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "note.txt"), []byte("hello"), 0644))

	testSuite := &testsuite.WorkflowTestSuite{}
	actEnv := testSuite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(env.EnvRunCommandActivity)
	actEnv.RegisterActivity(HibernateWorktreeActivity)
	actEnv.RegisterActivity(WakeWorktreeActivity)

	hibResult, err := actEnv.ExecuteActivity(HibernateWorktreeActivity, HibernateWorktreeInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: worktreeDir}},
		WorktreePath: worktreeDir,
		BranchName:   branchName,
	})
	require.NoError(t, err)
	var out HibernateWorktreeOutput
	require.NoError(t, hibResult.Get(&out))
	assert.Equal(t, filepath.Join(worktreeDir, HibernationMetadataFile), out.MetadataPath)
	assert.Equal(t, filepath.Join(worktreeDir, HibernationPatchFile), out.PatchPath)
	assert.True(t, IsWorktreeHibernated(worktreeDir))

	wakeResult, err := actEnv.ExecuteActivity(WakeWorktreeActivity, WakeWorktreeInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: worktreeDir}},
	})
	require.NoError(t, err)
	var wakeOut WakeWorktreeOutput
	require.NoError(t, wakeResult.Get(&wakeOut))
	assert.Equal(t, branchName, wakeOut.BranchName)
	assert.False(t, IsWorktreeHibernated(worktreeDir))

	content, err := os.ReadFile(filepath.Join(worktreeDir, "note.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestHibernateWorktreeActivity_EmptyBranchName(t *testing.T) {
	t.Parallel()
	testSuite := &testsuite.WorkflowTestSuite{}
	actEnv := testSuite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(HibernateWorktreeActivity)

	_, err := actEnv.ExecuteActivity(HibernateWorktreeActivity, HibernateWorktreeInput{
		BranchName: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch name is required")
}

func TestIsWorktreeHibernated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, IsWorktreeHibernated(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, HibernationMetadataFile), []byte("{}"), 0644))
	assert.True(t, IsWorktreeHibernated(dir))
}