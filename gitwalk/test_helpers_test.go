package gitwalk

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// setupTestRepo initializes a git repo in a temp directory, writes the
// given files (path -> content), and commits them on the `main` branch.
// Returns the repo's working tree directory.
func setupTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repoDir := t.TempDir()

	runGitInRepo(t, repoDir, "init", "-b", "main")
	runGitInRepo(t, repoDir, "config", "user.name", "Test User")
	runGitInRepo(t, repoDir, "config", "user.email", "test@example.com")
	runGitInRepo(t, repoDir, "config", "commit.gpgsign", "false")

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		writeFile(t, repoDir, p, files[p])
	}

	runGitInRepo(t, repoDir, "add", "-A")
	runGitInRepo(t, repoDir, "commit", "-m", "initial")

	hash := runGitInRepo(t, repoDir, "rev-parse", "HEAD")
	require.NotEmpty(t, hash)
	return repoDir
}
