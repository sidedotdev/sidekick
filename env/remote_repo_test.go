package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRemoteWorktreeActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := EnvContainer{Env: localEnv}

	t.Run("creates worktree successfully", func(t *testing.T) {
		t.Parallel()
		output, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/remote-test-feature",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.Contains(t, output.WorktreePath, "sidekick-worktrees")
		assert.DirExists(t, output.WorktreePath)

		cmd := exec.Command("git", "branch", "--show-current")
		cmd.Dir = output.WorktreePath
		branchOutput, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, "side/remote-test-feature", strings.TrimSpace(string(branchOutput)))
	})

	t.Run("creates worktree with start branch", func(t *testing.T) {
		t.Parallel()
		output, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/remote-from-main",
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.DirExists(t, output.WorktreePath)
	})

	t.Run("returns error for duplicate branch", func(t *testing.T) {
		t.Parallel()
		input := CreateRemoteWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/remote-dup-branch",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		}

		firstOutput, err := CreateRemoteWorktreeActivity(ctx, input)
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(firstOutput.WorktreePath)) })

		input.WorkspaceId = "ws-" + ksuid.New().String()
		_, err = CreateRemoteWorktreeActivity(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("strips side/ prefix for directory name", func(t *testing.T) {
		t.Parallel()
		output, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      repoDir,
			BranchName:   "side/remote-dir-test",
			WorkspaceId:  "ws-" + ksuid.New().String(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.Contains(t, output.WorktreePath, "remote-dir-test")
		assert.NotContains(t, output.WorktreePath, "side/")
	})
}

func TestSyncRepoToRemoteActivity_RequiresSSHCapableEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)

	_, err = SyncRepoToRemoteActivity(ctx, SyncRepoToRemoteInput{
		EnvContainer: EnvContainer{Env: localEnv},
		LocalRepoDir: repoDir,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support SSH-based repo sync")
}

func TestSyncRefspecs(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		[]string{"+refs/heads/main:refs/heads/main"},
		syncRefspecs("refs/heads/main", nil))
	assert.Equal(t,
		[]string{"+refs/heads/main:refs/heads/main", "+refs/heads/develop:refs/heads/develop"},
		syncRefspecs("refs/heads/main", []string{"develop", "main", "refs/heads/develop"}))
}
