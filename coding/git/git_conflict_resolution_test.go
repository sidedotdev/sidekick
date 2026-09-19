package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sidekick/env"
)

// setupConflictingBranches creates a repo where merging "feature" into "main"
// conflicts on conflict.txt, and leaves "main" checked out.
func setupConflictingBranches(t *testing.T, ctx context.Context) (string, env.EnvContainer) {
	t.Helper()
	repoDir := setupTestGitRepo(t)
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	createCommitWithFile(t, repoDir, "Initial commit", "conflict.txt", "base content")
	runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
	createCommitWithFile(t, repoDir, "Feature change", "conflict.txt", "feature content")
	runGitCommandInTestRepo(t, repoDir, "checkout", "main")
	createCommitWithFile(t, repoDir, "Main change", "conflict.txt", "main content")
	return repoDir, env.EnvContainer{Env: devEnv}
}

// runConflictingMerge runs a git merge that is expected to fail with conflicts.
func runConflictingMerge(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"merge"}, args...)...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "expected merge to conflict, got: %s", string(output))
	require.Contains(t, string(output), "CONFLICT")
}

func mergeHeadExists(t *testing.T, repoDir string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "MERGE_HEAD")
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// writeAndCommitLines writes a file with the given lines and commits it.
func writeAndCommitLines(t *testing.T, repoDir, message, filename string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, filename), []byte(content), 0644))
	runGitCommandInTestRepo(t, repoDir, "add", filename)
	runGitCommandInTestRepo(t, repoDir, "commit", "-m", message)
}

// numberedLines produces distinct lines so that separate edits land in separate
// diff hunks.
func numberedLines(count int) []string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	return lines
}

// setupFarApartConflictRepo creates a conflict on the first line of a long file,
// with the merge already attempted and conflict markers in the working tree.
func setupFarApartConflictRepo(t *testing.T, ctx context.Context) (string, env.EnvContainer) {
	t.Helper()
	repoDir := setupTestGitRepo(t)
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)

	base := numberedLines(20)
	writeAndCommitLines(t, repoDir, "Initial commit", "conflict.txt", base)

	featureLines := append([]string{}, base...)
	featureLines[0] = "feature line1"
	runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
	writeAndCommitLines(t, repoDir, "Feature change", "conflict.txt", featureLines)

	mainLines := append([]string{}, base...)
	mainLines[0] = "main line1"
	runGitCommandInTestRepo(t, repoDir, "checkout", "main")
	writeAndCommitLines(t, repoDir, "Main change", "conflict.txt", mainLines)

	runConflictingMerge(t, repoDir, "feature")
	return repoDir, env.EnvContainer{Env: devEnv}
}

// resolveWithUnrelatedEdit replaces the conflict block with a single resolved
// line and additionally edits a line far away from the conflict.
func resolveWithUnrelatedEdit(t *testing.T, repoDir string) {
	t.Helper()
	path := filepath.Join(repoDir, "conflict.txt")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var resolved []string
	inConflict := false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			inConflict = true
			resolved = append(resolved, "resolved line1")
		case strings.HasPrefix(line, ">>>>>>>"):
			inConflict = false
		case inConflict:
			// drop both sides of the conflict
		case line == "line15":
			resolved = append(resolved, "unrelated edit")
		default:
			resolved = append(resolved, line)
		}
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(resolved, "\n")+"\n"), 0644))
}

func TestGitConflictResolutionDiffActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("excludes hunks unrelated to the conflict", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupFarApartConflictRepo(t, ctx)

		snapshot, err := GitSnapshotConflictMarkersActivity(ctx, envContainer, GitSnapshotConflictMarkersParams{WorktreePath: repoDir})
		require.NoError(t, err)
		require.NotEmpty(t, snapshot.TreeHash)
		require.NotEmpty(t, snapshot.ConflictRegions)

		resolveWithUnrelatedEdit(t, repoDir)

		diff, err := GitConflictResolutionDiffActivity(ctx, envContainer, GitConflictResolutionDiffParams{
			WorktreePath: repoDir,
			Snapshot:     snapshot,
		})
		require.NoError(t, err)

		assert.Contains(t, diff, "resolved line1")
		assert.NotContains(t, diff, "unrelated edit")
		assert.Equal(t, 1, strings.Count(diff, "@@ -"), "expected exactly the conflict hunk, got:\n%s", diff)
	})

	t.Run("includes all hunks when no conflict regions were recorded", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupFarApartConflictRepo(t, ctx)

		snapshot, err := GitSnapshotConflictMarkersActivity(ctx, envContainer, GitSnapshotConflictMarkersParams{WorktreePath: repoDir})
		require.NoError(t, err)
		snapshot.ConflictRegions = nil

		resolveWithUnrelatedEdit(t, repoDir)

		diff, err := GitConflictResolutionDiffActivity(ctx, envContainer, GitConflictResolutionDiffParams{
			WorktreePath: repoDir,
			Snapshot:     snapshot,
		})
		require.NoError(t, err)

		assert.Contains(t, diff, "resolved line1")
		assert.Contains(t, diff, "unrelated edit")
	})

	t.Run("returns empty diff without a snapshot", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupFarApartConflictRepo(t, ctx)

		diff, err := GitConflictResolutionDiffActivity(ctx, envContainer, GitConflictResolutionDiffParams{
			WorktreePath: repoDir,
		})
		require.NoError(t, err)
		assert.Empty(t, diff)
	})
}

func TestGitMergeAbortActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("aborts conflicted regular merge", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		runConflictingMerge(t, repoDir, "feature")
		require.True(t, mergeHeadExists(t, repoDir))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		assert.False(t, mergeHeadExists(t, repoDir))
		assert.Empty(t, runGitCommandInTestRepo(t, repoDir, "status", "--porcelain"))
		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "main content", string(content))
	})

	t.Run("aborts conflicted squash merge without MERGE_HEAD", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		runConflictingMerge(t, repoDir, "--squash", "feature")
		// A conflicted squash merge leaves no MERGE_HEAD, so a plain
		// `git merge --abort` cannot clean it up.
		require.False(t, mergeHeadExists(t, repoDir))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		assert.Empty(t, runGitCommandInTestRepo(t, repoDir, "status", "--porcelain"))
		squashMsgPath := runGitCommandInTestRepo(t, repoDir, "rev-parse", "--git-path", "SQUASH_MSG")
		_, statErr := os.Stat(filepath.Join(repoDir, squashMsgPath))
		assert.True(t, os.IsNotExist(statErr), "expected SQUASH_MSG to be removed")
		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "main content", string(content))
	})

	t.Run("no-op preserves uncommitted changes when no merge in progress", func(t *testing.T) {
		t.Parallel()
		repoDir, envContainer := setupConflictingBranches(t, ctx)

		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("local edit"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("untracked"), 0644))

		err := GitMergeAbortActivity(ctx, envContainer, GitMergeAbortParams{WorktreePath: repoDir})
		require.NoError(t, err)

		content, err := os.ReadFile(filepath.Join(repoDir, "conflict.txt"))
		require.NoError(t, err)
		assert.Equal(t, "local edit", string(content))
		_, statErr := os.Stat(filepath.Join(repoDir, "untracked.txt"))
		assert.NoError(t, statErr)
	})
}
