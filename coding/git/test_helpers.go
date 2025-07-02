package git

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// setupTestGitRepo initializes a git repository in a temporary directory.
// It configures a default user and initializes with 'main' as the default branch.
func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()

	// Check if git is installed
	_, err := exec.LookPath("git")
	require.NoError(t, err, "git command not found in PATH")

	// git init with main branch
	cmdInit := exec.Command("git", "init", "-b", "main")
	cmdInit.Dir = repoDir
	outputInit, err := cmdInit.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(outputInit))

	// Configure user name and email for commits
	runGitCommandInTestRepo(t, repoDir, "config", "user.name", "Test User")
	runGitCommandInTestRepo(t, repoDir, "config", "user.email", "test@example.com")

	return repoDir
}

// runGitCommandInTestRepo is a helper to run git commands in the test repo directory.
// It uses require.NoError to fail the test immediately if a command fails.
func runGitCommandInTestRepo(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	// Trim space from output for easier comparison, but include full output in error
	trimmedOutput := strings.TrimSpace(string(output))
	require.NoError(t, err, "git command %v failed in %s:\n%s", args, repoDir, string(output))
	return trimmedOutput
}

// createCommit creates an empty commit with a given message.
func createCommit(t *testing.T, repoDir, message string) string {
	t.Helper()
	runGitCommandInTestRepo(t, repoDir, "commit", "--allow-empty", "-m", message)
	// Get the commit hash
	hash := runGitCommandInTestRepo(t, repoDir, "rev-parse", "HEAD")
	return hash
}
