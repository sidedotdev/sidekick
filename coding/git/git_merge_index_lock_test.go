package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mergeIndexLockEnv struct {
	env.Env
	t              *testing.T
	lockPath       string
	lockTime       time.Time
	releaseOnRetry bool
	withoutLock    bool
	attempts       int
}

func (e *mergeIndexLockEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	if input.Command != "sh" || !strings.Contains(strings.Join(input.Args, " "), "git merge ") {
		return e.Env.RunCommand(ctx, input)
	}

	e.attempts++
	if e.withoutLock {
		return env.EnvRunCommandOutput{ExitStatus: 128, Stderr: "fatal: Unable to write index.\n"}, nil
	}
	if e.attempts == 1 {
		require.NoError(e.t, os.WriteFile(e.lockPath, nil, 0644))
		require.NoError(e.t, os.Chtimes(e.lockPath, e.lockTime, e.lockTime))
	} else if e.releaseOnRetry {
		require.FileExists(e.t, e.lockPath, "a fresh lock must not be removed by recovery")
		require.NoError(e.t, os.Remove(e.lockPath))
	}

	out, err := e.Env.RunCommand(ctx, input)
	if e.attempts == 1 {
		require.NoError(e.t, err)
		require.NotZero(e.t, out.ExitStatus)
		require.Contains(e.t, out.Stderr, "Unable to write index.")
		// Older Git versions omit the lock-specific details from this failure.
		out.Stderr = "fatal: Unable to write index.\n"
	}
	return out, err
}

func TestGitMergeActivityGenericIndexWriteFailure(t *testing.T) {
	t.Parallel()

	for _, strategy := range []MergeStrategy{MergeStrategyMerge, MergeStrategySquash} {
		t.Run(string(strategy), func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name           string
				lockAge        time.Duration
				releaseOnRetry bool
				withoutLock    bool
			}{
				{name: "stale target lock", lockAge: 10 * time.Minute},
				{name: "live target lock", releaseOnRetry: true},
				{name: "no target lock", withoutLock: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					ctx := context.Background()
					repoDir := setupTestGitRepo(t)
					createFileAndCommit(t, repoDir, "seed.txt", "seed\n", "initial commit")
					targetDir := filepath.Join(t.TempDir(), "target with spaces")
					runGitCommandInTestRepo(t, repoDir, "worktree", "add", "-b", "target", targetDir)
					createFileAndCommit(t, targetDir, "target.txt", "target\n", "target commit")
					createFileAndCommit(t, repoDir, "feature.txt", "feature\n", "feature commit")
					sourceHead := runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")
					targetHead := runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD")
					targetLock := strings.TrimSpace(runGitCommandInTestRepo(t, targetDir,
						"rev-parse", "--path-format=absolute", "--git-path", "index.lock"))

					localEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
					require.NoError(t, err)
					mergeEnv := &mergeIndexLockEnv{
						Env:            localEnv,
						t:              t,
						lockPath:       targetLock,
						lockTime:       time.Now().Add(-tc.lockAge),
						releaseOnRetry: tc.releaseOnRetry,
						withoutLock:    tc.withoutLock,
					}
					sourceLock := writeIndexLock(t, repoDir, time.Now().Add(-10*time.Minute))

					result, err := GitMergeActivity(ctx, env.EnvContainer{Env: mergeEnv}, GitMergeParams{
						SourceBranch:  "main",
						TargetBranch:  "target",
						MergeStrategy: strategy,
					})
					if tc.withoutLock {
						require.ErrorContains(t, err, "merge failed in worktree: fatal: Unable to write index.")
						require.Equal(t, 1, mergeEnv.attempts, "an index-write failure without a target lock is not lock contention")
						require.Equal(t, targetHead, runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD"))
						require.NoFileExists(t, filepath.Join(targetDir, "feature.txt"))
					} else {
						require.NoError(t, err)
						require.False(t, result.HasConflicts)
						require.Equal(t, 2, mergeEnv.attempts)
						content, err := os.ReadFile(filepath.Join(targetDir, "feature.txt"))
						require.NoError(t, err)
						require.Equal(t, "feature\n", string(content))
						require.NotEqual(t, targetHead, runGitCommandInTestRepo(t, targetDir, "rev-parse", "HEAD"))
					}
					require.NoFileExists(t, targetLock)
					require.FileExists(t, sourceLock, "recovery must not remove another worktree's lock")
					require.Equal(t, sourceHead, runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD"))
					require.Empty(t, runGitCommandInTestRepo(t, targetDir, "status", "--porcelain"))
					content, err := os.ReadFile(filepath.Join(targetDir, "target.txt"))
					require.NoError(t, err)
					require.Equal(t, "target\n", string(content))
				})
			}
		})
	}
}
