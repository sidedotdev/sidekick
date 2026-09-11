package env

import (
	"context"
	"fmt"
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

	t.Run("reuses existing worktree with uncommitted changes on re-provisioning", func(t *testing.T) {
		t.Parallel()
		remoteRepoDir := setupTestGitRepo(t)
		localRepoDir := setupTestGitRepo(t)

		input := CreateRemoteWorktreeInput{
			EnvContainer: newRemoteEnvContainer(t, remoteRepoDir),
			RepoDir:      remoteRepoDir,
			BranchName:   "side/reprovisioned-branch",
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
			LocalRepoDir: localRepoDir,
		}
		first, err := CreateRemoteWorktreeActivity(ctx, input)
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(first.WorktreePath)) })

		gitOutput := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", first.WorktreePath}, args...)...)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "git %v: %s", args, out)
			return string(out)
		}
		trackedFile := filepath.Join(first.WorktreePath, "tracked.txt")
		require.NoError(t, os.WriteFile(trackedFile, []byte("committed\n"), 0644))
		gitOutput("add", "tracked.txt")
		gitOutput("commit", "-m", "flow progress")
		require.NoError(t, os.WriteFile(trackedFile, []byte("staged\n"), 0644))
		gitOutput("add", "tracked.txt")
		require.NoError(t, os.WriteFile(trackedFile, []byte("unstaged\n"), 0644))
		dirtyFile := filepath.Join(first.WorktreePath, "uncommitted.txt")
		require.NoError(t, os.WriteFile(dirtyFile, []byte("live work"), 0644))

		head := gitOutput("rev-parse", "HEAD")
		branch := gitOutput("symbolic-ref", "HEAD")
		index := gitOutput("ls-files", "--stage")
		reflog := gitOutput("reflog", "show", "HEAD")
		branchReflog := gitOutput("reflog", "show", input.BranchName)
		require.NotEmpty(t, gitOutput("diff", "--cached"))
		require.NotEmpty(t, gitOutput("diff"))

		for i := 0; i < 2; i++ {
			second, err := CreateRemoteWorktreeActivity(ctx, input)
			require.NoError(t, err)
			assert.Equal(t, first.WorktreePath, second.WorktreePath)
			assert.Equal(t, head, gitOutput("rev-parse", "HEAD"))
			assert.Equal(t, branch, gitOutput("symbolic-ref", "HEAD"))
			assert.Equal(t, index, gitOutput("ls-files", "--stage"))
			assert.Equal(t, "staged\n", gitOutput("show", ":tracked.txt"))
			assert.Equal(t, reflog, gitOutput("reflog", "show", "HEAD"))
			assert.Equal(t, branchReflog, gitOutput("reflog", "show", input.BranchName))
			content, err := os.ReadFile(trackedFile)
			require.NoError(t, err)
			assert.Equal(t, "unstaged\n", string(content))
			content, err = os.ReadFile(dirtyFile)
			require.NoError(t, err)
			assert.Equal(t, "live work", string(content))
		}
	})

	t.Run("refuses to clear dirty same-branch worktree at another path", func(t *testing.T) {
		t.Parallel()
		remoteRepoDir := setupTestGitRepo(t)
		localRepoDir := setupTestGitRepo(t)
		branchName := "side/dirty-stale-branch"

		dirtyWorktreePath := filepath.Join(t.TempDir(), "dirty-worktree")
		cmd := exec.Command("git", "-C", remoteRepoDir, "worktree", "add", "-b", branchName, dirtyWorktreePath, "main")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "worktree setup failed: %s", string(out))
		dirtyFile := filepath.Join(dirtyWorktreePath, "uncommitted.txt")
		require.NoError(t, os.WriteFile(dirtyFile, []byte("live work"), 0644))

		_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: newRemoteEnvContainer(t, remoteRepoDir),
			RepoDir:      remoteRepoDir,
			BranchName:   branchName,
			StartBranch:  "main",
			WorkspaceId:  "ws-" + ksuid.New().String(),
			LocalRepoDir: localRepoDir,
		})
		require.Error(t, err)
		assert.FileExists(t, dirtyFile, "uncommitted work must never be wiped")
	})

	t.Run("refuses existing same-name branch and worktree in sandbox", func(t *testing.T) {
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
		require.Error(t, err)
		assert.Empty(t, output.WorktreePath)
		assert.DirExists(t, staleWorktreePath)

		cmd = exec.Command("git", "-C", staleWorktreePath, "branch", "--show-current")
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

// TestSyncMergeResultToLocalOverSSHWorktreePathWithSpaces pins that the local
// worktree holding the target branch is located even when its path contains
// spaces, which whitespace-field parsing of porcelain output truncates. It
// must not be parallel: PATH is set.
func TestSyncMergeResultToLocalOverSSHWorktreePathWithSpaces(t *testing.T) {
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

	localRepoDir := setupTestGitRepo(t)
	branchName := "target-branch"
	gitRun(t, localRepoDir, "branch", branchName)

	worktreePath := filepath.Join(t.TempDir(), "Application Support", "target wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreePath), 0755))
	gitRun(t, localRepoDir, "worktree", "add", worktreePath, branchName)

	remoteRepoDir := filepath.Join(t.TempDir(), "remote")
	cloneOut, err := exec.Command("git", "clone", "--branch", branchName, localRepoDir, remoteRepoDir).CombinedOutput()
	require.NoError(t, err, "git clone failed: %s", string(cloneOut))
	gitRun(t, remoteRepoDir, "config", "user.name", "Test User")
	gitRun(t, remoteRepoDir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepoDir, "merged.txt"), []byte("merged content"), 0644))
	gitRun(t, remoteRepoDir, "add", "merged.txt")
	gitRun(t, remoteRepoDir, "commit", "-m", "merge result")
	remoteTip := gitRun(t, remoteRepoDir, "rev-parse", branchName)

	err = syncMergeResultToLocalOverSSH(ctx, []string{"fake-host"}, remoteRepoDir, localRepoDir, branchName)
	require.NoError(t, err)

	assert.Equal(t, remoteTip, gitRun(t, localRepoDir, "rev-parse", branchName))
	assert.FileExists(t, filepath.Join(worktreePath, "merged.txt"))
}

// TestSyncBranchToRemoteOverSSHWorktreePathWithSpaces covers the same
// porcelain parsing on the remote side, where the realigned worktree path may
// likewise contain spaces. It must not be parallel: PATH is set.
func TestSyncBranchToRemoteOverSSHWorktreePathWithSpaces(t *testing.T) {
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
	branchName := "side/remote-branch"
	gitRun(t, remoteRepoDir, "branch", branchName)

	remoteWorktreePath := filepath.Join(t.TempDir(), "Application Support", "remote wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(remoteWorktreePath), 0755))
	gitRun(t, remoteRepoDir, "worktree", "add", remoteWorktreePath, branchName)

	localRepoDir := filepath.Join(t.TempDir(), "local")
	cloneOut, err := exec.Command("git", "clone", "--branch", branchName, remoteRepoDir, localRepoDir).CombinedOutput()
	require.NoError(t, err, "git clone failed: %s", string(cloneOut))
	gitRun(t, localRepoDir, "config", "user.name", "Test User")
	gitRun(t, localRepoDir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(localRepoDir, "local.txt"), []byte("local content"), 0644))
	gitRun(t, localRepoDir, "add", "local.txt")
	gitRun(t, localRepoDir, "commit", "-m", "local commit")
	localTip := gitRun(t, localRepoDir, "rev-parse", branchName)

	err = syncBranchToRemoteOverSSH(ctx, []string{"fake-host"}, remoteRepoDir, localRepoDir, branchName)
	require.NoError(t, err)

	assert.Equal(t, localTip, gitRun(t, remoteRepoDir, "rev-parse", branchName))
	assert.FileExists(t, filepath.Join(remoteWorktreePath, "local.txt"))
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

// TestRemoteUnpackFailed pins the classification that starts push recovery
// (self-contained-pack retry, then fsck-gated re-seed): only remote-side
// unpack failures qualify, and only via their specific signatures.
func TestRemoteUnpackFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "unresolved deltas from unpack-objects",
			err: fmt.Errorf("git push to /root/sidekick: exit status 1: " +
				"remote: fatal: unresolved deltas left after unpacking\n" +
				"error: remote unpack failed: unpack-objects abnormal exit\n" +
				"! [remote rejected]   devpod -> devpod (unpacker error)"),
			want: true,
		},
		{
			name: "non-fast-forward rejection",
			err:  fmt.Errorf("git push: ! [rejected] main -> main (non-fast-forward)"),
			want: false,
		},
		{
			name: "unpacker error alone is too broad to classify",
			err:  fmt.Errorf("git push: ! [remote rejected] main -> main (unpacker error)"),
			want: false,
		},
		{
			name: "ssh transport failure",
			err:  fmt.Errorf("git push: ssh: connect to host example.com port 22: Connection refused"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, remoteUnpackFailed(tc.err))
		})
	}
}

func TestCreateRemoteWorktreeActivity_InvalidIndexConflictPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	branchName := "side/status-failure"
	out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", branchName, worktreePath, "main").CombinedOutput()
	require.NoError(t, err, "%s", out)

	indexOutput, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-path", "index").CombinedOutput()
	require.NoError(t, err, "%s", indexOutput)
	indexPath := strings.TrimSpace(string(indexOutput))
	require.NoError(t, os.WriteFile(indexPath, []byte("invalid index"), 0644))
	workFile := filepath.Join(worktreePath, "live.txt")
	require.NoError(t, os.WriteFile(workFile, []byte("live work"), 0644))

	out, err = exec.Command("git", "-C", worktreePath, "status", "--porcelain").CombinedOutput()
	require.Error(t, err, "fixture must fail Git status: %s", out)

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
		EnvContainer: EnvContainer{Env: localEnv},
		RepoDir:      repoDir, BranchName: branchName, StartBranch: "main",
		WorkspaceId: "ws-" + ksuid.New().String(), LocalRepoDir: setupTestGitRepo(t),
	})
	assert.Error(t, err)
	content, err := os.ReadFile(workFile)
	require.NoError(t, err)
	assert.Equal(t, "live work", string(content))
	content, err = os.ReadFile(indexPath)
	require.NoError(t, err)
	assert.Equal(t, "invalid index", string(content))
}

func TestCreateRemoteWorktreeActivity_HiddenUntrackedConflictPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	branchName := "side/hidden-untracked"
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	gitRun("worktree", "add", "-b", branchName, worktreePath, "main")
	gitRun("config", "status.showUntrackedFiles", "no")
	workFile := filepath.Join(worktreePath, "live.txt")
	require.NoError(t, os.WriteFile(workFile, []byte("live work"), 0644))

	out, err := exec.Command("git", "-C", worktreePath, "status", "--porcelain").CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Empty(t, string(out), "fixture must hide untracked work")

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
		EnvContainer: EnvContainer{Env: localEnv},
		RepoDir:      repoDir, BranchName: branchName, StartBranch: "main",
		WorkspaceId: "ws-" + ksuid.New().String(), LocalRepoDir: setupTestGitRepo(t),
	})
	require.Error(t, err)
	content, err := os.ReadFile(workFile)
	require.NoError(t, err)
	assert.Equal(t, "live work", string(content))
}

func TestCreateRemoteWorktreeActivity_HiddenSubmoduleConflictPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	submoduleRepo := setupTestGitRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	branchName := "side/hidden-submodule"
	gitRun := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	gitRun(repoDir, "worktree", "add", "-b", branchName, worktreePath, "main")
	gitRun(worktreePath, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepo, "module")
	gitRun(worktreePath, "commit", "-am", "add submodule")
	gitRun(worktreePath, "config", "submodule.module.ignore", "all")
	moduleDir := filepath.Join(worktreePath, "module")
	workFile := filepath.Join(moduleDir, "live.txt")
	require.NoError(t, os.WriteFile(workFile, []byte("committed"), 0644))
	gitRun(moduleDir, "add", "live.txt")
	gitRun(moduleDir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "module baseline")
	gitRun(worktreePath, "-c", "submodule.module.ignore=none", "add", "module")
	gitRun(worktreePath, "commit", "-m", "update module")
	require.NoError(t, os.WriteFile(workFile, []byte("live work"), 0644))
	require.Empty(t, gitRun(worktreePath, "status", "--porcelain"))
	require.NotEmpty(t, gitRun(worktreePath, "status", "--porcelain", "--ignore-submodules=none"))

	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
		EnvContainer: EnvContainer{Env: localEnv},
		RepoDir:      repoDir, BranchName: branchName, StartBranch: "main",
		WorkspaceId: "ws-" + ksuid.New().String(), LocalRepoDir: setupTestGitRepo(t),
	})
	require.Error(t, err)
	content, err := os.ReadFile(workFile)
	require.NoError(t, err)
	assert.Equal(t, "live work", string(content))
}

// provisioningBoundaryEnv isolates HOME and permits writes at command boundaries.
type provisioningBoundaryEnv struct {
	Env
	home         string
	afterCommand func(EnvRunCommandInput)
}

func (e *provisioningBoundaryEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	if input.Command == "sh" && len(input.Args) == 2 && input.Args[1] == "echo $HOME" {
		return EnvRunCommandOutput{Stdout: e.home}, nil
	}
	output, err := e.Env.RunCommand(ctx, input)
	if e.afterCommand != nil {
		e.afterCommand(input)
	}
	return output, err
}

func TestCreateRemoteWorktreeActivity_PreservesConflicts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "worktree", "branch", "write-after-inspection"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir := setupTestGitRepo(t)
			localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
			require.NoError(t, err)
			e := &provisioningBoundaryEnv{Env: localEnv, home: t.TempDir()}
			branch := "side/conflict"
			target := filepath.Join(e.home, "sidekick-worktrees", "ws", filepath.Base(repoDir)+"-conflict")
			gitRun := func(dir string, args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
				require.NoError(t, err, "git %v: %s", args, out)
				return string(out)
			}
			var file, head, index string
			if kind == "branch" {
				gitRun(repoDir, "checkout", "-b", branch)
				gitRun(repoDir, "commit", "--allow-empty", "-m", "remote progress")
				head = gitRun(repoDir, "rev-parse", branch)
				gitRun(repoDir, "checkout", "main")
			} else if kind == "directory" {
				require.NoError(t, os.MkdirAll(target, 0755))
				file = filepath.Join(target, "live.txt")
				require.NoError(t, os.WriteFile(file, []byte("live work"), 0644))
			} else {
				gitRun(repoDir, "worktree", "add", "-b", "side/other", target, "main")
				file = filepath.Join(target, "live.txt")
				require.NoError(t, os.WriteFile(file, []byte("baseline"), 0644))
				gitRun(target, "add", "live.txt")
				gitRun(target, "commit", "-m", "baseline")
				head = gitRun(target, "rev-parse", "HEAD")
				index = gitRun(target, "ls-files", "--stage")
			}
			injected := false
			e.afterCommand = func(input EnvRunCommandInput) {
				if kind != "write-after-inspection" || input.Command != "sh" ||
					!strings.Contains(strings.Join(input.Args, " "), "symbolic-ref") {
					return
				}
				require.False(t, injected)
				injected = true
				require.NoError(t, os.WriteFile(file, []byte("staged"), 0644))
				gitRun(target, "add", "live.txt")
				index = gitRun(target, "ls-files", "--stage")
				require.NoError(t, os.WriteFile(file, []byte("unstaged"), 0644))
			}
			_, err = CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
				EnvContainer: EnvContainer{Env: e},
				RepoDir:      repoDir, BranchName: branch, StartBranch: "main",
				WorkspaceId: "ws", LocalRepoDir: setupTestGitRepo(t),
			})
			require.Error(t, err)
			switch kind {
			case "branch":
				assert.Equal(t, head, gitRun(repoDir, "rev-parse", branch))
			case "directory":
				content, err := os.ReadFile(file)
				require.NoError(t, err)
				assert.Equal(t, "live work", string(content))
			default:
				assert.Equal(t, head, gitRun(target, "rev-parse", "HEAD"))
				assert.Equal(t, index, gitRun(target, "ls-files", "--stage"))
				content, err := os.ReadFile(file)
				require.NoError(t, err)
				if kind == "write-after-inspection" {
					require.True(t, injected)
					assert.Equal(t, "unstaged", string(content))
					assert.Equal(t, "staged", gitRun(target, "show", ":live.txt"))
				} else {
					assert.Equal(t, "baseline", string(content))
				}
			}
		})
	}
}

func TestCreateRemoteWorktreeActivity_ReusesWithoutLocalRepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	localEnv, err := NewLocalEnv(ctx, LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	e := &provisioningBoundaryEnv{Env: localEnv, home: t.TempDir()}
	input := CreateRemoteWorktreeInput{
		EnvContainer: EnvContainer{Env: e},
		RepoDir:      repoDir,
		BranchName:   "side/repeated",
		StartBranch:  "main",
		WorkspaceId:  "ws",
	}
	first, err := CreateRemoteWorktreeActivity(ctx, input)
	require.NoError(t, err)
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", first.WorktreePath}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	file := filepath.Join(first.WorktreePath, "live.txt")
	require.NoError(t, os.WriteFile(file, []byte("baseline"), 0644))
	gitRun("add", "live.txt")
	gitRun("commit", "-m", "flow progress")
	require.NoError(t, os.WriteFile(file, []byte("staged"), 0644))
	gitRun("add", "live.txt")
	require.NoError(t, os.WriteFile(file, []byte("unstaged"), 0644))
	head := gitRun("rev-parse", "HEAD")
	index := gitRun("ls-files", "--stage")
	reflog := gitRun("reflog", "show", "HEAD")
	for i := 0; i < 2; i++ {
		repeated, err := CreateRemoteWorktreeActivity(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, first.WorktreePath, repeated.WorktreePath)
		assert.Equal(t, head, gitRun("rev-parse", "HEAD"))
		assert.Equal(t, index, gitRun("ls-files", "--stage"))
		assert.Equal(t, reflog, gitRun("reflog", "show", "HEAD"))
		assert.Equal(t, "staged", gitRun("show", ":live.txt"))
		content, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, "unstaged", string(content))
	}
}
