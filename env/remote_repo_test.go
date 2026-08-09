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
	"go.temporal.io/sdk/temporal"
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

func TestCreateRemoteWorktreeActivity_LocalBranchReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gitRevParse := func(t *testing.T, repoDir, ref string) string {
		t.Helper()
		cmd := exec.Command("git", "-C", repoDir, "rev-parse", ref)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git rev-parse %s failed: %s", ref, string(out))
		return strings.TrimSpace(string(out))
	}

	newRemoteEnvContainer := func(t *testing.T, repoDir string) EnvContainer {
		t.Helper()
		remoteEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		return EnvContainer{Env: remoteEnv}
	}

	t.Run("reserves branch in local repo at start point", func(t *testing.T) {
		t.Parallel()
		remoteRepoDir := setupTestGitRepo(t)
		localRepoDir := setupTestGitRepo(t)

		output, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: newRemoteEnvContainer(t, remoteRepoDir),
			RepoDir:      remoteRepoDir,
			BranchName:   "side/reserved-branch",
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
			LocalRepoDir: localRepoDir,
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.DirExists(t, output.WorktreePath)

		assert.Equal(t,
			gitRevParse(t, localRepoDir, "main"),
			gitRevParse(t, localRepoDir, "side/reserved-branch"),
			"local branch should be reserved at the start point",
		)

		cmd := exec.Command("git", "-C", localRepoDir, "branch", "--show-current")
		branchOut, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, "main", strings.TrimSpace(string(branchOut)), "local repo should stay on its original branch")
	})

	t.Run("existing local branch yields branch already exists error", func(t *testing.T) {
		t.Parallel()
		remoteRepoDir := setupTestGitRepo(t)
		localRepoDir := setupTestGitRepo(t)

		cmd := exec.Command("git", "-C", localRepoDir, "branch", "side/taken-branch")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git branch failed: %s", string(out))

		_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: newRemoteEnvContainer(t, remoteRepoDir),
			RepoDir:      remoteRepoDir,
			BranchName:   "side/taken-branch",
			WorkspaceId:  "ws-" + ksuid.New().String(),
			LocalRepoDir: localRepoDir,
		})
		require.Error(t, err)
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, ErrTypeBranchAlreadyExists, appErr.Type())
	})

	t.Run("tolerates stale same-name branch and worktree in sandbox", func(t *testing.T) {
		t.Parallel()
		remoteRepoDir := setupTestGitRepo(t)
		localRepoDir := setupTestGitRepo(t)
		branchName := "side/stale-branch"

		staleWorktreePath := filepath.Join(t.TempDir(), "stale-worktree")
		cmd := exec.Command("git", "-C", remoteRepoDir, "worktree", "add", "-b", branchName, staleWorktreePath, "main")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "stale worktree setup failed: %s", string(out))

		output, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: newRemoteEnvContainer(t, remoteRepoDir),
			RepoDir:      remoteRepoDir,
			BranchName:   branchName,
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
			LocalRepoDir: localRepoDir,
		})
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(output.WorktreePath)) })
		assert.DirExists(t, output.WorktreePath)

		cmd = exec.Command("git", "-C", output.WorktreePath, "branch", "--show-current")
		branchOut, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, branchName, strings.TrimSpace(string(branchOut)))
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

// TestSyncFlowBranchToLocalOverSSH exercises the real git-over-ssh transport
// between two local fixture repos, with a fake ssh on PATH that runs the
// requested remote command locally. It must not be parallel: PATH is set.
func TestSyncFlowBranchToLocalOverSSH(t *testing.T) {
	ctx := context.Background()
	installFakeSSH(t, `for a in "$@"; do cmd="$a"; done
exec sh -c "$cmd"
`)

	gitRun := func(t *testing.T, repoDir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
		return strings.TrimSpace(string(out))
	}

	remoteRepoDir := setupTestGitRepo(t)
	localRepoDir := setupTestGitRepo(t)
	branchName := "side/backup-branch"

	require.NoError(t, os.WriteFile(filepath.Join(localRepoDir, "local.txt"), []byte("local content"), 0644))
	gitRun(t, localRepoDir, "add", "local.txt")
	gitRun(t, localRepoDir, "commit", "-m", "local main commit")

	// The local branch starts out diverged from the remote one, so only a
	// forced update can bring it to the remote tip.
	gitRun(t, localRepoDir, "branch", branchName)
	gitRun(t, remoteRepoDir, "checkout", "-b", branchName)
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepoDir, "remote.txt"), []byte("remote content"), 0644))
	gitRun(t, remoteRepoDir, "add", "remote.txt")
	gitRun(t, remoteRepoDir, "commit", "-m", "remote flow commit")
	remoteTip := gitRun(t, remoteRepoDir, "rev-parse", branchName)

	err := syncFlowBranchToLocalOverSSH(ctx, []string{"fake-host"}, remoteRepoDir, localRepoDir, branchName)
	require.NoError(t, err)

	assert.Equal(t, remoteTip, gitRun(t, localRepoDir, "rev-parse", branchName))
	assert.Equal(t, "main", gitRun(t, localRepoDir, "branch", "--show-current"))
	assert.Empty(t, gitRun(t, localRepoDir, "status", "--porcelain"))
	assert.NoFileExists(t, filepath.Join(localRepoDir, "remote.txt"))
	assert.FileExists(t, filepath.Join(localRepoDir, "local.txt"))
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
