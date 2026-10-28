package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func conflictSwitchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func TestConflictSnapshotSurvivesPruning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, container := setupFarApartConflictRepo(t, ctx)
	snapshot, err := GitSnapshotConflictMarkersActivity(ctx, container, GitSnapshotConflictMarkersParams{WorktreePath: dir})
	require.NoError(t, err)

	resolveWithUnrelatedEdit(t, dir)
	conflictSwitchGit(t, dir, "add", "conflict.txt")
	conflictSwitchGit(t, dir, "prune", "--expire=now")

	diff, err := GitConflictResolutionDiffActivity(ctx, container, GitConflictResolutionDiffParams{
		WorktreePath: dir,
		Snapshot:     snapshot,
	})
	require.NoError(t, err)
	assert.Contains(t, diff, "resolved line1")
	assert.NotContains(t, diff, "unrelated edit")
}

func TestConflictSnapshotSurvivesEnvironmentSwitch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		resolution string
		splitIndex bool
		abort      bool
	}{
		{name: "unresolved"},
		{name: "unresolved split index", splitIndex: true},
		{name: "resolved and staged", resolution: "resolved content"},
		{name: "resolved without changes to HEAD", resolution: "main content"},
		{name: "abort after switch", abort: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			sourceDir, source := setupConflictingBranches(t, ctx)
			ours := conflictSwitchGit(t, sourceDir, "rev-parse", "HEAD")
			theirs := conflictSwitchGit(t, sourceDir, "rev-parse", "feature")
			runConflictingMerge(t, sourceDir, "--no-commit", "--no-ff", "feature")
			snapshot, err := GitSnapshotConflictMarkersActivity(ctx, source, GitSnapshotConflictMarkersParams{WorktreePath: sourceDir})
			require.NoError(t, err)

			if tc.resolution != "" {
				require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "conflict.txt"), []byte(tc.resolution), 0644))
				conflictSwitchGit(t, sourceDir, "add", "conflict.txt")
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "staged.bin"), []byte{0, 1, 2, 3}, 0644))
				conflictSwitchGit(t, sourceDir, "add", "staged.bin")
			}
			if tc.splitIndex {
				conflictSwitchGit(t, sourceDir, "update-index", "--split-index")
			}

			wantIndex := conflictSwitchGit(t, sourceDir, "ls-files", "--stage")
			wantStatus := conflictSwitchGit(t, sourceDir, "status", "--porcelain", "--untracked-files=all")
			wantContent, err := os.ReadFile(filepath.Join(sourceDir, "conflict.txt"))
			require.NoError(t, err)
			wantDiff, err := GitConflictResolutionDiffActivity(ctx, source, GitConflictResolutionDiffParams{Snapshot: snapshot})
			require.NoError(t, err)

			current := source
			for hop := 0; hop < 2; hop++ {
				exported, err := env.ExportForSwitchActivity(ctx, env.ExportForSwitchInput{EnvContainer: current, Branch: "main"})
				require.NoError(t, err)
				require.NotEmpty(t, exported.Bundle.WIPArchivePath)
				t.Cleanup(func() { _ = os.Remove(exported.Bundle.WIPArchivePath) })
				assert.Equal(t, wantIndex, conflictSwitchGit(t, current.Env.GetWorkingDirectory(), "ls-files", "--stage"))

				targetDir := filepath.Join(t.TempDir(), "target")
				conflictSwitchGit(t, sourceDir, "clone", "--no-local", "--single-branch", "--branch", "main", sourceDir, targetDir)
				conflictSwitchGit(t, targetDir, "config", "user.name", "Test User")
				conflictSwitchGit(t, targetDir, "config", "user.email", "test@example.com")
				for _, object := range []string{snapshot.TreeHash, theirs} {
					cmd := exec.Command("git", "cat-file", "-e", object)
					cmd.Dir = targetDir
					require.Error(t, cmd.Run(), "the fresh clone must not already contain %s", object)
				}

				current = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: targetDir}}
				_, err = env.RestoreFromSwitchActivity(ctx, env.RestoreFromSwitchInput{EnvContainer: current, Bundle: exported.Bundle})
				require.NoError(t, err)

				diff, err := GitConflictResolutionDiffActivity(ctx, current, GitConflictResolutionDiffParams{Snapshot: snapshot})
				require.NoError(t, err)
				assert.Equal(t, wantDiff, diff)
				assert.Equal(t, wantIndex, conflictSwitchGit(t, targetDir, "ls-files", "--stage"))
				assert.Equal(t, wantStatus, conflictSwitchGit(t, targetDir, "status", "--porcelain", "--untracked-files=all"))
				assert.Equal(t, theirs, conflictSwitchGit(t, targetDir, "rev-parse", "--verify", "MERGE_HEAD"))
				content, err := os.ReadFile(filepath.Join(targetDir, "conflict.txt"))
				require.NoError(t, err)
				assert.Equal(t, wantContent, content)
				if tc.resolution == "" {
					content, err := os.ReadFile(filepath.Join(targetDir, "staged.bin"))
					require.NoError(t, err)
					assert.Equal(t, []byte{0, 1, 2, 3}, content)
				}
				conflictSwitchGit(t, targetDir, "cat-file", "-e", theirs+"^{tree}")
			}

			dir := current.Env.GetWorkingDirectory()
			if tc.abort {
				conflictSwitchGit(t, dir, "merge", "--abort")
				assert.Equal(t, ours, conflictSwitchGit(t, dir, "rev-parse", "HEAD"))
				assert.Empty(t, conflictSwitchGit(t, dir, "status", "--porcelain"))
				return
			}

			require.NoError(t, os.WriteFile(filepath.Join(dir, "conflict.txt"), []byte("resolved content\n"), 0644))
			conflictSwitchGit(t, dir, "add", "conflict.txt")
			diff, err := GitConflictResolutionDiffActivity(ctx, current, GitConflictResolutionDiffParams{Snapshot: snapshot})
			require.NoError(t, err)
			assert.Contains(t, diff, "+resolved content")
			assert.Contains(t, diff, "-<<<<<<<")
			conflictSwitchGit(t, dir, "commit", "--no-edit")
			assert.Equal(t, ours+" "+theirs, conflictSwitchGit(t, dir, "show", "-s", "--format=%P", "HEAD"))
		})
	}
}

func TestConflictSnapshotReleasedAfterFinalization(t *testing.T) {
	t.Parallel()
	for _, finish := range []string{"commit", "abort"} {
		t.Run(finish, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dir, container := setupConflictingBranches(t, ctx)
			otherDir := filepath.Join(t.TempDir(), "other")
			conflictSwitchGit(t, dir, "worktree", "add", "--detach", otherDir, "HEAD")
			runConflictingMerge(t, dir, "feature")
			_, err := GitSnapshotConflictMarkersActivity(ctx, container, GitSnapshotConflictMarkersParams{})
			require.NoError(t, err)

			runConflictingMerge(t, otherDir, "feature")
			other := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: otherDir}}
			otherSnapshot, err := GitSnapshotConflictMarkersActivity(ctx, other, GitSnapshotConflictMarkersParams{})
			require.NoError(t, err)

			if finish == "commit" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "conflict.txt"), []byte("resolved\n"), 0644))
				attempts := 0
				err = runActivityWithRetries(t, func(activityCtx context.Context) error {
					if err := GitCommitMergeActivity(activityCtx, container, GitCommitMergeParams{
						CommitterName:  "Test User",
						CommitterEmail: "test@example.com",
					}); err != nil {
						return err
					}
					attempts++
					if attempts == 1 {
						return fmt.Errorf("simulate lost completion")
					}
					return nil
				})
				require.NoError(t, err)
			} else {
				require.NoError(t, GitMergeAbortActivity(ctx, container, GitMergeAbortParams{}))
				require.NoError(t, GitMergeAbortActivity(ctx, container, GitMergeAbortParams{}))
			}

			exported, err := env.ExportForSwitchActivity(ctx, env.ExportForSwitchInput{EnvContainer: container, Branch: "main"})
			require.NoError(t, err)
			if exported.Bundle.WIPArchivePath != "" {
				t.Cleanup(func() { _ = os.Remove(exported.Bundle.WIPArchivePath) })
			}
			assert.Empty(t, exported.Bundle.WIPArchivePath, "a finalized clean worktree must not retain conflict snapshot pins")

			conflictSwitchGit(t, otherDir, "prune", "--expire=now")
			_, err = GitConflictResolutionDiffActivity(ctx, other, GitConflictResolutionDiffParams{Snapshot: otherSnapshot})
			require.NoError(t, err, "finalizing one worktree must preserve another worktree's snapshot")
		})
	}
}
