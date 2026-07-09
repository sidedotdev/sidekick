package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeIndexLock(t *testing.T, repoDir string, mtime time.Time) string {
	t.Helper()
	lockPath := filepath.Join(repoDir, ".git", "index.lock")
	require.NoError(t, os.WriteFile(lockPath, nil, 0644))
	require.NoError(t, os.Chtimes(lockPath, mtime, mtime))
	return lockPath
}

func TestRunGitIndexCommandWithRetry_RemovesStaleLock(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "seed.txt", "seed\n", "initial commit")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.txt"), []byte("hello\n"), 0644))

	// Simulate a git process killed mid-write: an orphaned lock old enough to
	// be past the grace period.
	lockPath := writeIndexLock(t, repoDir, time.Now().Add(-10*time.Minute))

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	require.NoError(t, GitAddActivity(ctx, GitAddActivityInput{EnvContainer: envContainer, Path: "app.txt"}))

	require.True(t, stagedFiles(t, repoDir)["app.txt"], "file should be staged after stale lock recovery")
	_, statErr := os.Stat(lockPath)
	require.True(t, os.IsNotExist(statErr), "stale index.lock should have been removed")
}

func TestCleanupStaleIndexLock_LeavesFreshLock(t *testing.T) {
	t.Parallel()

	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "seed.txt", "seed\n", "initial commit")

	// A lock touched just now could belong to a live git writer and must not
	// be removed.
	lockPath := writeIndexLock(t, repoDir, time.Now())

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	removed, err := cleanupStaleIndexLock(ctx, envContainer, "", nil)
	require.NoError(t, err)
	require.False(t, removed, "a fresh lock must not be treated as stale")
	require.FileExists(t, lockPath, "fresh index.lock must be left in place")
}

func TestIsIndexLockContentionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output env.EnvRunCommandActivityOutput
		want   bool
	}{
		{
			name:   "index lock contention on stderr",
			output: env.EnvRunCommandActivityOutput{Stderr: "fatal: Unable to create '/repo/.git/index.lock': File exists."},
			want:   true,
		},
		{
			name:   "unrelated failure",
			output: env.EnvRunCommandActivityOutput{Stderr: "error: pathspec 'missing' did not match any files"},
			want:   false,
		},
		{
			name:   "different lock file",
			output: env.EnvRunCommandActivityOutput{Stderr: "fatal: Unable to create '/repo/.git/HEAD.lock': File exists."},
			want:   false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isIndexLockContentionError(tt.output))
		})
	}
}
