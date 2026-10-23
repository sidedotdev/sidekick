package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// installFakeSSH puts an ssh stand-in on PATH whose behavior is given by
// script (a /bin/sh body receiving ssh's arguments), so the "remote"
// repository is just another local directory. Callers must not be parallel.
func installFakeSSH(t *testing.T, script string) {
	t.Helper()
	installFakeTool(t, "ssh", script)
}

// installFakeTool prepends a shim to PATH for a tool the Linux sandbox has
// but the developer machine may not.
func installFakeTool(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func syncTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func syncTestCommit(t *testing.T, dir, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	syncTestGit(t, dir, "add", name)
	syncTestGit(t, dir, "commit", "-m", name)
	return syncTestGit(t, dir, "rev-parse", "HEAD")
}

func setupGitSyncRepos(t *testing.T) (local, remote, worktree string) {
	t.Helper()
	installFakeSSH(t, `for a in "$@"; do cmd="$a"; done
exec sh -c "$cmd"
`)
	if _, err := exec.LookPath("flock"); err != nil {
		// util-linux flock gates advancing the main checkout in place; the
		// sandbox has it but macOS does not. Like util-linux, the shim locks
		// the inherited descriptor so the lock outlives the shim process.
		installFakeTool(t, "flock", `for a in "$@"; do fd="$a"; done
exec perl -e 'use Fcntl qw(:flock); open(my $fh, ">&=", $ARGV[0]) or exit 1; exit(flock($fh, LOCK_EX | LOCK_NB) ? 0 : 1)' "$fd"
`)
	}
	local = setupTestGitRepo(t)
	syncTestGit(t, local, "commit", "--allow-empty", "-m", "Initial commit")
	remote = filepath.Join(t.TempDir(), "remote repo")
	syncTestGit(t, local, "clone", "--no-local", local, remote)
	syncTestGit(t, remote, "config", "user.name", "Test")
	syncTestGit(t, remote, "config", "user.email", "test@example.com")
	worktree = filepath.Join(t.TempDir(), "flow worktree")
	syncTestGit(t, remote, "worktree", "add", "-b", "side/flow", worktree)
	syncTestGit(t, local, "branch", "side/flow")
	return local, remote, worktree
}

func TestGitSyncBacksUpCommitsAndRefreshesBase(t *testing.T) {
	local, remote, worktree := setupGitSyncRepos(t)
	flowTip := syncTestCommit(t, worktree, "flow.txt", "agent committed directly")
	baseTip := syncTestCommit(t, local, "base.txt", "new base")

	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))

	assert.Equal(t, flowTip, syncTestGit(t, local, "rev-parse", "side/flow"))
	assert.Equal(t, flowTip, syncTestGit(t, worktree, "rev-parse", "HEAD"))
	assert.Equal(t, baseTip, syncTestGit(t, remote, "rev-parse", "main"))
	assert.Equal(t, baseTip, syncTestGit(t, remote, "rev-parse", "refs/remotes/side-worker/main"))
	assert.FileExists(t, filepath.Join(remote, "base.txt"))
	assert.NoFileExists(t, filepath.Join(worktree, "base.txt"))
	assert.Empty(t, syncTestGit(t, remote, "status", "--porcelain"))

	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))
	assert.Equal(t, flowTip, syncTestGit(t, local, "rev-parse", "side/flow"))
}

func TestGitSyncPreservesUnsafeBaseCheckouts(t *testing.T) {
	for _, mode := range []string{"unstaged", "staged", "untracked", "diverged", "merge"} {
		t.Run(mode, func(t *testing.T) {
			local, remote, worktree := setupGitSyncRepos(t)
			baseTip := syncTestCommit(t, local, "base.txt", "worker version")
			switch mode {
			case "unstaged", "staged", "untracked":
				require.NoError(t, os.WriteFile(filepath.Join(remote, "base.txt"), []byte("sandbox work"), 0644))
				if mode != "untracked" {
					syncTestGit(t, remote, "add", "base.txt")
					if mode == "unstaged" {
						syncTestGit(t, remote, "commit", "-m", "sandbox base")
						require.NoError(t, os.WriteFile(filepath.Join(remote, "base.txt"), []byte("more work"), 0644))
					}
				}
			case "diverged":
				syncTestCommit(t, remote, "other.txt", "sandbox commit")
			case "merge":
				gitDir := syncTestGit(t, remote, "rev-parse", "--absolute-git-dir")
				require.NoError(t, os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(syncTestGit(t, remote, "rev-parse", "HEAD")+"\n"), 0644))
			}
			head := syncTestGit(t, remote, "rev-parse", "HEAD")
			status := syncTestGit(t, remote, "status", "--porcelain")
			diff := syncTestGit(t, remote, "diff", "HEAD")

			require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))

			assert.Equal(t, head, syncTestGit(t, remote, "rev-parse", "HEAD"))
			assert.Equal(t, status, syncTestGit(t, remote, "status", "--porcelain"))
			assert.Equal(t, diff, syncTestGit(t, remote, "diff", "HEAD"))
			assert.Equal(t, baseTip, syncTestGit(t, remote, "rev-parse", "refs/remotes/side-worker/main"))
			if mode == "untracked" {
				content, err := os.ReadFile(filepath.Join(remote, "base.txt"))
				require.NoError(t, err)
				assert.Equal(t, "sandbox work", string(content))
			}
		})
	}
}

func TestGitSyncPreservesRewrittenAndRestoredFlowHistory(t *testing.T) {
	local, _, worktree := setupGitSyncRepos(t)
	first := syncTestCommit(t, worktree, "flow.txt", "first")
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))

	require.NoError(t, os.WriteFile(filepath.Join(worktree, "flow.txt"), []byte("amended"), 0644))
	syncTestGit(t, worktree, "add", "flow.txt")
	syncTestGit(t, worktree, "commit", "--amend", "-m", "amended")
	amended := syncTestGit(t, worktree, "rev-parse", "HEAD")
	require.NotEqual(t, first, amended)
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))

	for _, tip := range []string{first, amended} {
		assert.NotEmpty(t, syncTestGit(t, local, "for-each-ref", "--contains="+tip, "--format=%(refname)", "refs/sidekick/"))
	}

	syncTestGit(t, worktree, "reset", "--hard", first)
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))
	assert.NotEmpty(t, syncTestGit(t, local, "for-each-ref", "--contains="+amended, "--format=%(refname)", "refs/sidekick/"))
}

func TestGitSyncPreservesIgnoredFiles(t *testing.T) {
	local, remote, worktree := setupGitSyncRepos(t)
	exclude := filepath.Join(remote, ".git", "info", "exclude")
	require.NoError(t, os.WriteFile(exclude, []byte("base.txt\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "base.txt"), []byte("ignored sandbox work"), 0644))
	head := syncTestGit(t, remote, "rev-parse", "HEAD")
	tip := syncTestCommit(t, local, "base.txt", "worker content")

	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))

	content, err := os.ReadFile(filepath.Join(remote, "base.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ignored sandbox work", string(content))
	assert.Equal(t, head, syncTestGit(t, remote, "rev-parse", "HEAD"))
	assert.Equal(t, tip, syncTestGit(t, remote, "rev-parse", workerRefPrefix+"main"))
}

func TestGitSyncSkipsCheckoutWithoutExclusiveAccess(t *testing.T) {
	local, remote, worktree := setupGitSyncRepos(t)
	head := syncTestGit(t, remote, "rev-parse", "HEAD")
	tip := syncTestCommit(t, local, "base.txt", "worker content")
	lockPath := filepath.Join(t.TempDir(), "checkout.lock")
	lock, err := os.Create(lockPath)
	require.NoError(t, err)
	defer lock.Close()
	require.NoError(t, unix.Flock(int(lock.Fd()), unix.LOCK_SH))
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)

	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, lockPath))

	assert.Equal(t, head, syncTestGit(t, remote, "rev-parse", "HEAD"))
	assert.NoFileExists(t, filepath.Join(remote, "base.txt"))
	assert.Equal(t, tip, syncTestGit(t, remote, "rev-parse", workerRefPrefix+"main"))
}

func TestGitSyncUnchangedSkipsTransfers(t *testing.T) {
	local, _, worktree := setupGitSyncRepos(t)
	syncTestCommit(t, worktree, "flow.txt", "committed")
	logPath := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("GIT_SYNC_TEST_LOG", logPath)
	installFakeSSH(t, `for a in "$@"; do cmd="$a"; done
printf '%s\n' "$cmd" >> "$GIT_SYNC_TEST_LOG"
exec sh -c "$cmd"
`)
	lock := filepath.Join(local, ".git", "sync-test.lock")
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, lock))
	require.NoError(t, os.WriteFile(logPath, nil, 0644))

	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, lock))

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "upload-pack")
	assert.NotContains(t, string(data), "receive-pack")
	assert.Equal(t, 1, strings.Count(string(data), "\n"))
}

func TestGitSyncBackgroundDoesNotMutateCheckedOutBranches(t *testing.T) {
	local, remote, worktree := setupGitSyncRepos(t)
	head := syncTestGit(t, remote, "rev-parse", "HEAD")
	tip := syncTestCommit(t, local, "base.txt", "worker version")
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, ""))
	assert.Equal(t, head, syncTestGit(t, remote, "rev-parse", "HEAD"))
	assert.Equal(t, tip, syncTestGit(t, remote, "rev-parse", workerRefPrefix+"main"))

	other := filepath.Join(t.TempDir(), "other worktree")
	syncTestGit(t, remote, "worktree", "add", "-b", "other", other)
	syncTestGit(t, local, "branch", "other", "main")
	require.NoError(t, syncGitRepositoriesWithLock(context.Background(), []string{"fake-host"}, local, worktree, filepath.Join(local, ".git", "sync-test.lock")))
	assert.Equal(t, head, syncTestGit(t, other, "rev-parse", "HEAD"))
	assert.Equal(t, tip, syncTestGit(t, other, "rev-parse", workerRefPrefix+"other"))
}

// remoteSyncTestEnv stands in for a sandbox whose commands and sync transport
// both run locally through the fake ssh.
type remoteSyncTestEnv struct {
	env.Env
	worktree     string
	localRepoDir string
}

func (e *remoteSyncTestEnv) GetType() env.EnvType               { return env.EnvTypeModal }
func (e *remoteSyncTestEnv) GetWorkingDirectory() string        { return e.worktree }
func (e *remoteSyncTestEnv) WorkerRepository() (string, string) { return e.localRepoDir, "sandbox" }
func (e *remoteSyncTestEnv) SSHArgs(context.Context) ([]string, error) {
	return []string{"fake-host"}, nil
}
func (e *remoteSyncTestEnv) SSHConnConfig(context.Context) (env.SSHConnConfig, error) {
	return env.SSHConnConfig{}, nil
}
func (e *remoteSyncTestEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	cmd := exec.CommandContext(ctx, input.Command, input.Args...)
	cmd.Dir = e.worktree
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return env.EnvRunCommandOutput{Stderr: string(out), ExitStatus: exitErr.ExitCode()}, nil
	}
	return env.EnvRunCommandOutput{Stdout: string(out)}, err
}

func TestRemoteSyncObserverBacksUpCommandCommits(t *testing.T) {
	local, _, worktree := setupGitSyncRepos(t)
	e := &remoteSyncTestEnv{worktree: worktree, localRepoDir: local}
	key, _ := gitSyncOperation(e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, repositoryGitSync.checkpoint(ctx, key, worktree, func(context.Context, bool) error { return nil }))
	})

	activities := &env.Activities{CommandObserver: RemoteSyncObserver{}}
	output, err := activities.RunCommand(context.Background(), env.EnvRunCommandActivityInput{
		EnvContainer: env.EnvContainer{Env: e},
		Command:      "git",
		Args:         []string{"commit", "--allow-empty", "-m", "agent commit"},
	})
	require.NoError(t, err)
	require.Zero(t, output.ExitStatus, output.Stderr)
	tip := syncTestGit(t, worktree, "rev-parse", "HEAD")
	require.Eventually(t, func() bool {
		return syncTestGit(t, local, "rev-parse", "side/flow") == tip
	}, 5*time.Second, 10*time.Millisecond)
}
