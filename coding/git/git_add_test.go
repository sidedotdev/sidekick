package git

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stagedFiles returns the set of paths currently staged in the index.
func stagedFiles(t *testing.T, repoDir string) map[string]bool {
	t.Helper()
	out := runGitCommandInTestRepo(t, repoDir, "diff", "--cached", "--name-only")
	staged := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			staged[line] = true
		}
	}
	return staged
}

func TestGitAddActivity_SkipsUntrackedBinaries(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "tracked.txt", "original\n", "initial commit")

	// Modify a tracked file (should be staged).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "tracked.txt"), []byte("modified\n"), fs.FileMode(0644)))

	// Add an untracked text file (should be staged).
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "new.txt"), []byte("new text\n"), fs.FileMode(0644)))

	// Add an untracked binary file (should NOT be staged, but left on disk).
	binaryPath := filepath.Join(repoDir, "image.bin")
	require.NoError(t, os.WriteFile(binaryPath, []byte{0x00, 0x01, 0x02, 0x00, 0xff}, fs.FileMode(0644)))

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	require.NoError(t, GitAddActivity(ctx, GitAddActivityInput{EnvContainer: envContainer, Path: "."}))

	staged := stagedFiles(t, repoDir)
	require.True(t, staged["tracked.txt"], "modified tracked file should be staged")
	require.True(t, staged["new.txt"], "untracked non-binary file should be staged")
	require.False(t, staged["image.bin"], "untracked binary file should not be staged")

	// The untracked binary must remain on disk untouched.
	_, statErr := os.Stat(binaryPath)
	require.NoError(t, statErr, "untracked binary file should still exist on disk")
}

func TestGitAddActivity_StagesTrackedBinary(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "tracked.txt", "original\n", "initial commit")

	// Commit a binary file so it is already tracked.
	binaryPath := filepath.Join(repoDir, "image.bin")
	require.NoError(t, os.WriteFile(binaryPath, []byte{0x00, 0x01, 0x02, 0x00, 0xff}, fs.FileMode(0644)))
	runGitCommandInTestRepo(t, repoDir, "add", "image.bin")
	runGitCommandInTestRepo(t, repoDir, "commit", "-m", "add binary")

	// Modify the already-tracked binary file (should still be staged).
	require.NoError(t, os.WriteFile(binaryPath, []byte{0x00, 0x09, 0x08, 0x00, 0x07}, fs.FileMode(0644)))

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	require.NoError(t, GitAddActivity(ctx, GitAddActivityInput{EnvContainer: envContainer, Path: "."}))

	staged := stagedFiles(t, repoDir)
	require.True(t, staged["image.bin"], "modified tracked binary file should be staged")
}
