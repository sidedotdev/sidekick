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
