package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"

	"github.com/rs/zerolog/log"
)

// staleIndexLockGraceSeconds is how old a git index.lock must be before it is
// eligible for automatic removal. A legitimate in-progress index write holds
// the lock for a fraction of a second, so a lock lingering this long is
// effectively always orphaned by a git process that was killed mid-operation
// (e.g. an activity cancellation or worker shutdown that SIGKILLed git).
const staleIndexLockGraceSeconds = 120

// staleIndexLockCleanupScript removes a git index.lock only after confirming it
// is stale: it must be older than the grace period and its mtime must remain
// unchanged across a short re-check, since an active writer would still be
// touching it. It exits 0 (printing "removed <path>") only when it removes the
// lock, and non-zero otherwise. stat is invoked in both GNU (-c %Y) and BSD
// (-f %m) forms so it works on Linux containers and macOS alike.
const staleIndexLockCleanupScript = `
lock=$(git rev-parse --git-path index.lock 2>/dev/null)
[ -n "$lock" ] || exit 2
[ -e "$lock" ] || exit 3
now=$(date +%%s)
mt=$(stat -c %%Y "$lock" 2>/dev/null || stat -f %%m "$lock" 2>/dev/null)
[ -n "$mt" ] || exit 4
age=$((now - mt))
[ "$age" -ge %d ] || exit 5
sleep 1
[ -e "$lock" ] || exit 3
mt2=$(stat -c %%Y "$lock" 2>/dev/null || stat -f %%m "$lock" 2>/dev/null)
[ "$mt" = "$mt2" ] || exit 6
rm -f "$lock" && echo "removed $lock"
`

// isIndexLockContentionError reports whether git command output indicates the
// command failed solely because an index.lock already exists.
func isIndexLockContentionError(output env.EnvRunCommandActivityOutput) bool {
	combined := output.Stdout + "\n" + output.Stderr
	return strings.Contains(combined, "index.lock") && strings.Contains(combined, "File exists")
}

// cleanupStaleIndexLock removes a confirmed-stale index.lock from the given
// worktree, returning whether a lock was actually removed. worktreeDir is the
// directory whose repository owns the lock; pass "" to use the environment's
// working directory. It never removes a lock that could belong to a live git
// process (see staleIndexLockCleanupScript).
func cleanupStaleIndexLock(ctx context.Context, envContainer env.EnvContainer, worktreeDir string, envVars []string) (bool, error) {
	script := fmt.Sprintf(staleIndexLockCleanupScript, staleIndexLockGraceSeconds)
	if worktreeDir != "" {
		script = fmt.Sprintf("cd %s && {\n%s\n}", shellQuote(worktreeDir), script)
	}
	output, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "sh",
		Args:               []string{"-c", script},
		EnvVars:            envVars,
	})
	if err != nil {
		return false, fmt.Errorf("failed to run stale index.lock cleanup: %w", err)
	}
	return output.ExitStatus == 0 && strings.Contains(output.Stdout, "removed "), nil
}

// runGitIndexCommandWithRetry runs an index-mutating git command via the
// environment. If it fails only because a stale index.lock is present, the
// confirmed-stale lock is removed and the command is retried once. cleanupDir
// is the worktree the lock lives in; pass "" to use the environment's working
// directory (the same directory the command runs in).
func runGitIndexCommandWithRetry(ctx context.Context, input env.EnvRunCommandActivityInput, cleanupDir string) (env.EnvRunCommandActivityOutput, error) {
	output, err := env.EnvRunCommandActivity(ctx, input)
	if err != nil || output.ExitStatus == 0 || !isIndexLockContentionError(output) {
		return output, err
	}

	removed, cleanupErr := cleanupStaleIndexLock(ctx, input.EnvContainer, cleanupDir, input.EnvVars)
	if cleanupErr != nil {
		log.Warn().Err(cleanupErr).Msg("failed to clean up stale git index.lock")
		return output, err
	}
	if !removed {
		return output, err
	}

	log.Info().Msg("removed stale git index.lock; retrying git command")
	return env.EnvRunCommandActivity(ctx, input)
}
