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

// worktreeState is everything a switch must carry over, in a form comparable
// across worktrees.
type worktreeState struct {
	Head         string
	Branch       string
	Status       string
	UnstagedDiff string
	StagedDiff   string
	Files        map[string]string
}

const missingFile = "<missing>"

func captureWorktreeState(t *testing.T, dir string, files []string) worktreeState {
	t.Helper()
	state := worktreeState{
		Head:         strings.TrimSpace(runGitIn(t, dir, "rev-parse", "HEAD")),
		Branch:       strings.TrimSpace(runGitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")),
		Status:       runGitIn(t, dir, "status", "--porcelain", "--untracked-files=all"),
		UnstagedDiff: runGitIn(t, dir, "diff"),
		StagedDiff:   runGitIn(t, dir, "diff", "--cached"),
		Files:        map[string]string{},
	}
	for _, name := range files {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			state.Files[name] = missingFile
			continue
		}
		require.NoError(t, err, name)
		state.Files[name] = string(content)
	}
	return state
}

// setupSwitchTestRepo returns a repo whose flow worktree has commits beyond
// the start point, so restoring onto a stale checkout is observable.
func setupSwitchTestRepo(t *testing.T) (repoDir, worktreeDir, branchName string) {
	t.Helper()
	repoDir, worktreeDir, branchName = setupHibernationTestRepo(t)
	writeTestFile(t, filepath.Join(worktreeDir, "tracked.txt"), "tracked")
	writeTestFile(t, filepath.Join(worktreeDir, "to_delete.txt"), "to delete")
	writeTestFile(t, filepath.Join(worktreeDir, "staged.txt"), "staged")
	runGitIn(t, worktreeDir, "add", ".")
	runGitIn(t, worktreeDir, "commit", "-m", "flow work")
	return repoDir, worktreeDir, branchName
}

func TestSwitchRoundTripBetweenWorktrees(t *testing.T) {
	t.Parallel()

	files := []string{"README.md", "tracked.txt", "to_delete.txt", "staged.txt", "new_staged.txt", "sub/untracked.txt"}
	cases := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		wantWIP bool
	}{
		{
			name:    "clean tree",
			prepare: func(t *testing.T, dir string) {},
		},
		{
			name: "tracked, staged, deleted and untracked text changes",
			prepare: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "tracked.txt"), "tracked edit")
				require.NoError(t, os.Remove(filepath.Join(dir, "to_delete.txt")))
				writeTestFile(t, filepath.Join(dir, "staged.txt"), "staged edit")
				runGitIn(t, dir, "add", "staged.txt")
				writeTestFile(t, filepath.Join(dir, "staged.txt"), "staged edit then unstaged edit")
				writeTestFile(t, filepath.Join(dir, "new_staged.txt"), "new staged")
				runGitIn(t, dir, "add", "new_staged.txt")
				writeTestFile(t, filepath.Join(dir, "sub", "untracked.txt"), "untracked text")
			},
			wantWIP: true,
		},
		{
			name: "deletion only",
			prepare: func(t *testing.T, dir string) {
				require.NoError(t, os.Remove(filepath.Join(dir, "to_delete.txt")))
			},
			wantWIP: true,
		},
		{
			name: "staged only",
			prepare: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "staged.txt"), "staged edit")
				runGitIn(t, dir, "add", "staged.txt")
			},
			wantWIP: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir, sourceDir, branch := setupSwitchTestRepo(t)
			tc.prepare(t, sourceDir)
			before := captureWorktreeState(t, sourceDir, files)

			exported, err := ExportForSwitchActivity(ctx, ExportForSwitchInput{
				EnvContainer: EnvContainer{Env: &LocalGitWorktreeEnv{WorkingDirectory: sourceDir}},
				Branch:       branch,
			})
			require.NoError(t, err)
			bundle := exported.Bundle
			if bundle.WIPArchivePath != "" {
				t.Cleanup(func() { _ = os.Remove(bundle.WIPArchivePath) })
			}

			resolvedRepoDir, err := filepath.EvalSymlinks(repoDir)
			require.NoError(t, err)
			assert.Equal(t, branch, bundle.Branch)
			assert.Equal(t, before.Head, bundle.HeadCommit)
			assert.Equal(t, resolvedRepoDir, bundle.LocalRepoDir)
			if tc.wantWIP {
				require.NotEmpty(t, bundle.WIPArchivePath)
				assert.FileExists(t, bundle.WIPArchivePath)
			} else {
				assert.Empty(t, bundle.WIPArchivePath)
			}

			assert.Equal(t, before, captureWorktreeState(t, sourceDir, files), "export must not mutate the source worktree")

			// Attach the same branch in a fresh worktree whose checkout is
			// stale, as a provisioned target would be before restore.
			runGitIn(t, repoDir, "worktree", "remove", "--force", sourceDir)
			targetDir := filepath.Join(t.TempDir(), "target")
			runGitIn(t, repoDir, "worktree", "add", targetDir, branch)
			runGitIn(t, targetDir, "reset", "--hard", "HEAD~1")

			targetContainer := EnvContainer{Env: &LocalGitWorktreeEnv{WorkingDirectory: targetDir}}
			_, err = RestoreFromSwitchActivity(ctx, RestoreFromSwitchInput{EnvContainer: targetContainer, Bundle: bundle})
			require.NoError(t, err)

			assert.Equal(t, before, captureWorktreeState(t, targetDir, files))
			assert.NoFileExists(t, filepath.Join(targetDir, HibernationPatchFile))
		})
	}
}

func TestExportForSwitchInPlaceLocalEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, _, _ := setupHibernationTestRepo(t)
	branch := strings.TrimSpace(runGitIn(t, repoDir, "rev-parse", "--abbrev-ref", "HEAD"))
	writeTestFile(t, filepath.Join(repoDir, "untracked.txt"), "untracked")

	bundle, err := (&LocalEnv{WorkingDirectory: repoDir}).ExportForSwitch(ctx, branch)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundle.WIPArchivePath) })

	resolvedRepoDir, err := filepath.EvalSymlinks(repoDir)
	require.NoError(t, err)
	assert.Equal(t, resolvedRepoDir, bundle.LocalRepoDir)
	assert.Equal(t, strings.TrimSpace(runGitIn(t, repoDir, "rev-parse", "HEAD")), bundle.HeadCommit)
	assert.NotEmpty(t, bundle.WIPArchivePath)
}

func TestExportForSwitchRejectsOtherCheckedOutBranch(t *testing.T) {
	t.Parallel()
	_, worktreeDir, _ := setupHibernationTestRepo(t)

	_, err := (&LocalGitWorktreeEnv{WorkingDirectory: worktreeDir}).ExportForSwitch(context.Background(), "side/some-other-branch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "side/some-other-branch")
}

func TestExportForSwitchRequiresLocalRepoDirForRemoteEnvs(t *testing.T) {
	t.Parallel()
	_, err := (&DevPodEnv{WorkingDirectory: "/workspaces/repo", WorkspaceName: "ws"}).ExportForSwitch(context.Background(), "side/branch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local repo dir")
}

func TestRestoreFromSwitchBacksUpLocalWIP(t *testing.T) {
	t.Parallel()

	for _, dirty := range []bool{false, true} {
		name := "clean"
		if dirty {
			name = "dirty"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir, targetDir, branch := setupSwitchTestRepo(t)
			sourceDir := filepath.Join(t.TempDir(), "source")
			runGitIn(t, repoDir, "worktree", "add", "--detach", sourceDir, branch)
			writeTestFile(t, filepath.Join(sourceDir, "tracked.txt"), "modal tracked edit")
			writeTestFile(t, filepath.Join(sourceDir, "shared-untracked.txt"), "modal untracked edit")
			source := &LocalGitWorktreeEnv{WorkingDirectory: sourceDir}
			archive, err := captureWIPToLocalFile(ctx, source)
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(archive) })
			bundle := SwitchBundle{
				Branch:         branch,
				HeadCommit:     strings.TrimSpace(runGitIn(t, sourceDir, "rev-parse", "HEAD")),
				LocalRepoDir:   repoDir,
				WIPArchivePath: archive,
			}
			files := []string{"tracked.txt", "shared-untracked.txt", "local-only.bin"}
			want := captureWorktreeState(t, sourceDir, files)
			want.Branch = branch

			if dirty {
				writeTestFile(t, filepath.Join(targetDir, "tracked.txt"), "local staged edit")
				runGitIn(t, targetDir, "add", "tracked.txt")
				writeTestFile(t, filepath.Join(targetDir, "tracked.txt"), "local unstaged edit")
				writeTestFile(t, filepath.Join(targetDir, "shared-untracked.txt"), "local untracked edit")
				writeTestFile(t, filepath.Join(targetDir, "local-only.bin"), "\x00\x01\xff")
			}
			before := captureWorktreeState(t, targetDir, files)
			target := &LocalGitWorktreeEnv{WorkingDirectory: targetDir}
			require.NoError(t, target.RestoreFromSwitch(ctx, bundle))
			assert.Equal(t, want, captureWorktreeState(t, targetDir, files))

			if !dirty {
				assert.Empty(t, runGitIn(t, targetDir, "stash", "list"))
				return
			}
			message := runGitIn(t, repoDir, "log", "-1", "--format=%s", "refs/stash")
			assert.Contains(t, message, "sidekick: local backup before environment switch")
			assert.Contains(t, message, branch)
			assert.Contains(t, message, bundle.HeadCommit)

			recoveryDir := filepath.Join(t.TempDir(), "recover")
			runGitIn(t, repoDir, "worktree", "add", "--detach", recoveryDir, before.Head)
			runGitIn(t, recoveryDir, "stash", "apply", "--index", "refs/stash")
			recovered := captureWorktreeState(t, recoveryDir, files)
			recovered.Branch = before.Branch
			assert.Equal(t, before, recovered)
		})
	}
}

func TestRestoreFromSwitchAbortsWhenLocalBackupFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir, dir, branch := setupSwitchTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "tracked.txt"), "local work")
	writeTestFile(t, filepath.Join(dir, "untracked.txt"), "local untracked")
	before := captureWorktreeState(t, dir, []string{"tracked.txt", "untracked.txt"})
	lock := strings.TrimSpace(runGitIn(t, repoDir, "rev-parse", "--path-format=absolute", "--git-path", "refs/stash.lock"))
	require.NoError(t, os.WriteFile(lock, nil, 0600))

	target := &LocalGitWorktreeEnv{WorkingDirectory: dir}
	err := target.RestoreFromSwitch(ctx, SwitchBundle{Branch: branch, HeadCommit: before.Head})
	require.Error(t, err)
	assert.Equal(t, before, captureWorktreeState(t, dir, []string{"tracked.txt", "untracked.txt"}))
}
