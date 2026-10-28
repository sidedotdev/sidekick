package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type switchWIPTestEnv struct {
	*LocalEnv
	envType      EnvType
	localRepoDir string
}

func (e *switchWIPTestEnv) GetType() EnvType {
	return e.envType
}

func TestSaveEnvSwitchWIP(t *testing.T) {
	t.Parallel()
	for _, envType := range []EnvType{EnvTypeLocal, EnvTypeLocalGitWorktree, EnvTypeDevPod} {
		t.Run(string(envType), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir, dir, branch := setupSwitchTestRepo(t)
			writeTestFile(t, filepath.Join(dir, "staged.txt"), "user stash\n")
			runGitIn(t, dir, "stash", "push", "-m", "user work")
			userStashes := runGitIn(t, dir, "stash", "list", "--format=%H %gs")

			writeTestFile(t, filepath.Join(dir, "tracked.txt"), "staged\n")
			runGitIn(t, dir, "add", "tracked.txt")
			writeTestFile(t, filepath.Join(dir, "tracked.txt"), "unstaged\n")
			writeTestFile(t, filepath.Join(dir, "untracked.bin"), "\x00\x01\xff")
			require.NoError(t, os.Symlink("tracked.txt", filepath.Join(dir, "untracked-link")))
			require.NoError(t, os.Remove(filepath.Join(dir, "to_delete.txt")))
			files := []string{"tracked.txt", "staged.txt", "to_delete.txt", "untracked.bin", "untracked-link"}
			before := captureWorktreeState(t, dir, files)
			input := SaveEnvSwitchWIPInput{
				EnvContainer: EnvContainer{Env: &switchWIPTestEnv{
					LocalEnv: &LocalEnv{WorkingDirectory: dir},
					envType:  envType,
				}},
				Branch:     branch,
				HeadCommit: before.Head,
			}

			_, err := SaveEnvSwitchWIPActivity(ctx, input)
			require.NoError(t, err)
			assert.Empty(t, runGitIn(t, dir, "status", "--porcelain", "--untracked-files=all"))
			assert.Equal(t, before.Head, strings.TrimSpace(runGitIn(t, dir, "rev-parse", "HEAD")))
			assert.Equal(t, userStashes, runGitIn(t, dir, "stash", "list", "--format=%H %gs"))
			refs := strings.Fields(runGitIn(t, repoDir, "for-each-ref", "--format=%(refname)", "refs/side/env-switch-wip/"))
			require.Len(t, refs, 1)
			message := runGitIn(t, repoDir, "log", "-1", "--format=%s", refs[0])
			assert.Contains(t, message, "WIP")
			assert.Contains(t, message, "environment switch")
			assert.Contains(t, message, branch)

			_, err = SaveEnvSwitchWIPActivity(ctx, input)
			require.NoError(t, err)
			assert.Equal(t, refs, strings.Fields(runGitIn(t, repoDir, "for-each-ref", "--format=%(refname)", "refs/side/env-switch-wip/")))
			assert.Equal(t, userStashes, runGitIn(t, dir, "stash", "list", "--format=%H %gs"))

			runGitIn(t, repoDir, "reflog", "expire", "--expire=now", "--all")
			runGitIn(t, repoDir, "gc", "--prune=now")
			runGitIn(t, dir, "stash", "apply", "--index", refs[0])
			assert.Equal(t, before, captureWorktreeState(t, dir, files))
			target, err := os.Readlink(filepath.Join(dir, "untracked-link"))
			require.NoError(t, err)
			assert.Equal(t, "tracked.txt", target)
		})
	}
}

func TestSaveEnvSwitchWIPRetainsStashIfPrivateRefFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, dir, branch := setupSwitchTestRepo(t)
	runGitIn(t, repoDir, "update-ref", "refs/side/env-switch-wip", "HEAD")
	writeTestFile(t, filepath.Join(dir, "tracked.txt"), "recoverable WIP\n")
	before := captureWorktreeState(t, dir, []string{"tracked.txt"})
	_, err := SaveEnvSwitchWIPActivity(ctx, SaveEnvSwitchWIPInput{
		EnvContainer: EnvContainer{Env: &LocalGitWorktreeEnv{WorkingDirectory: dir}},
		Branch:       branch,
		HeadCommit:   before.Head,
	})
	require.Error(t, err)
	runGitIn(t, dir, "stash", "apply", "--index", "refs/stash")
	assert.Equal(t, before, captureWorktreeState(t, dir, []string{"tracked.txt"}))
}

func TestSaveEnvSwitchWIPAllowsNextRemoteExport(t *testing.T) {
	installFakeSSH(t, `for a in "$@"; do cmd="$a"; done
exec sh -c "$cmd"
`)
	ctx := context.Background()
	repoDir, dir, branch := setupSwitchTestRepo(t)
	remoteDir := filepath.Join(t.TempDir(), "remote")
	runGitIn(t, repoDir, "clone", "--branch", branch, repoDir, remoteDir)
	writeTestFile(t, filepath.Join(dir, "tracked.txt"), "agent WIP\n")
	source := &LocalGitWorktreeEnv{WorkingDirectory: dir}
	bundle, err := source.ExportForSwitch(ctx, branch)
	require.NoError(t, err)
	require.NotEmpty(t, bundle.WIPArchivePath)
	t.Cleanup(func() { _ = os.Remove(bundle.WIPArchivePath) })

	remote := &switchWIPTestEnv{
		LocalEnv:     &LocalEnv{WorkingDirectory: remoteDir},
		envType:      EnvTypeModal,
		localRepoDir: repoDir,
	}
	require.NoError(t, restoreFromSwitch(ctx, remote, bundle))
	_, err = SaveEnvSwitchWIPActivity(ctx, SaveEnvSwitchWIPInput{
		EnvContainer: EnvContainer{Env: source},
		Branch:       branch,
		HeadCommit:   bundle.HeadCommit,
	})
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(remoteDir, "tracked.txt"), "agent WIP plus remote work\n")
	runGitIn(t, remoteDir, "add", ".")
	runGitIn(t, remoteDir, "commit", "-m", "continue after environment switch")
	remoteBundle, err := exportForSwitch(ctx, remote, branch, repoDir)
	require.NoError(t, err)
	require.Empty(t, remoteBundle.WIPArchivePath)
	require.NoError(t, source.RestoreFromSwitch(ctx, remoteBundle))
	assert.Equal(t, runGitIn(t, remoteDir, "rev-parse", "HEAD"), runGitIn(t, dir, "rev-parse", "HEAD"))
	assert.Equal(t, "agent WIP plus remote work\n", runGitIn(t, dir, "show", "HEAD:tracked.txt"))
	assert.Empty(t, runGitIn(t, dir, "status", "--porcelain"))
}

func TestRestoreFromSwitchPreservesDevPodWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, dir, branch := setupSwitchTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "tracked.txt"), "local WIP\n")
	before := captureWorktreeState(t, dir, []string{"tracked.txt"})
	target := &switchWIPTestEnv{
		LocalEnv: &LocalEnv{WorkingDirectory: dir},
		envType:  EnvTypeDevPod,
	}
	require.NoError(t, restoreFromSwitch(ctx, target, SwitchBundle{
		Branch: branch, HeadCommit: before.Head, LocalRepoDir: repoDir,
	}))
	refs := strings.Fields(runGitIn(t, repoDir, "for-each-ref", "--format=%(refname)", "refs/side/env-switch-wip/"))
	require.Len(t, refs, 1)
	runGitIn(t, dir, "stash", "apply", "--index", refs[0])
	assert.Equal(t, before, captureWorktreeState(t, dir, []string{"tracked.txt"}))
}

func (e *switchWIPTestEnv) SyncFlowBranchToLocal(ctx context.Context, branch string) error {
	return syncFlowBranchToLocalOverSSH(ctx, []string{"fake-host"}, e.WorkingDirectory, e.localRepoDir, branch)
}
