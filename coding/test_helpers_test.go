package coding

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()

	_, err := exec.LookPath("git")
	require.NoError(t, err, "git command not found in PATH")

	cmdInit := exec.Command("git", "init", "-b", "main")
	cmdInit.Dir = repoDir
	output, err := cmdInit.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(output))

	return repoDir
}

func runGit(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(output))
	return string(output)
}

func createFileAndCommit(t *testing.T, repoDir, filename, content, commitMsg string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(repoDir, filename), []byte(content), 0644)
	require.NoError(t, err)
	runGit(t, repoDir, "add", filename)
	runGit(t, repoDir, "commit", "-m", commitMsg)
}
