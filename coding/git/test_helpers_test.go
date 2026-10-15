package git

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sidekick/internal/testfixture"

	"github.com/stretchr/testify/require"
)

// setupTestGitRepo supplies an isolated repository with an unborn main branch.
// The package's TestMain supplies the default Git identity.
func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	require.NoError(t, emptyRepoFixture.CopyTo(repoDir))
	return repoDir
}

// runGitCommandInTestRepo is a helper to run git commands in the test repo directory.
// It uses require.NoError to fail the test immediately if a command fails.
func runGitCommandInTestRepo(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	// Default identity environment variables
	env := []string{
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	}
	return runGitCommandWithEnv(t, repoDir, env, args...)
}

// runGitCommandWithEnv runs a git command with specific environment variables.
func runGitCommandWithEnv(t *testing.T, repoDir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	// Trim space from output for easier comparison, but include full output in error
	trimmedOutput := strings.TrimSpace(string(output))
	require.NoError(t, err, "git command %v failed in %s:\n%s", args, repoDir, string(output))
	return trimmedOutput
}

// createCommitWithDate creates an empty commit with a given message and timestamp.
func createCommitWithDate(t *testing.T, repoDir, message string, commitTime time.Time) string {
	t.Helper()

	// Format time as ISO 8601 / RFC 3339
	dateStr := commitTime.Format(time.RFC3339)

	env := []string{
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=" + dateStr,
		"GIT_COMMITTER_DATE=" + dateStr,
	}

	runGitCommandWithEnv(t, repoDir, env, "commit", "--allow-empty", "-m", message)
	// Get the commit hash
	hash := runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")
	return hash
}

// createCommit creates an empty commit with a given message.
func createCommit(t *testing.T, repoDir, message string) string {
	t.Helper()
	runGitCommandInTestRepo(t, repoDir, "commit", "--allow-empty", "-m", message)
	// Get the commit hash
	hash := runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")
	return hash
}

var emptyRepoFixture = testfixture.Git("git init -q -b main")
