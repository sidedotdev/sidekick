package git

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitCommitActivity_CommitAll(t *testing.T) {
	t.Parallel()
	// Create a temporary git repository
	repoDir, cleanup := createTempGitRepo(t)
	defer cleanup()

	// new untracked file in the repository
	err := os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("test"), fs.FileMode(0644))
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// add all to track it
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	_, err = cmd.Output()
	require.NoError(t, err)

	// Make some changes in the repository
	err = os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("test2"), fs.FileMode(0644))
	require.NoError(t, err)

	// Create a context for the activity (doesn't have to be a real temporal one)
	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	// Call the GitCommitActivity function with CommitAll set to true and a commit message
	commitMessage := "Test commit"
	_, err = GitCommitActivity(ctx, envContainer, GitCommitParams{
		CommitMessage: commitMessage,
		CommitAll:     true,
	})
	require.NoError(t, err)

	// Check if the commit was successful by running the 'git log' command
	cmd = exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	require.NoError(t, err)

	// Check if the commit message is correct
	if strings.TrimSpace(string(output)) != commitMessage {
		t.Fatalf("expected commit message '%s', got '%s'", commitMessage, output)
	}
}

func TestGitCommitActivity_NoCommitAll(t *testing.T) {
	t.Parallel()
	// Create a temporary git repository
	repoDir, cleanup := createTempGitRepo(t)
	defer cleanup()

	// new untracked file in the repository
	err := os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("test"), fs.FileMode(0644))
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// add file to track it
	cmd := exec.Command("git", "add", "test.txt")
	cmd.Dir = repoDir
	_, err = cmd.Output()
	require.NoError(t, err)

	// Make some changes in the repository
	err = os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("test2"), fs.FileMode(0644))
	require.NoError(t, err)

	// Create a context for the activity (doesn't have to be a real temporal one)
	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	// Call the GitCommitActivity function with CommitAll set to false and a commit message
	commitMessage := "Test commit"
	_, err = GitCommitActivity(ctx, envContainer, GitCommitParams{
		CommitMessage: commitMessage,
		CommitAll:     false,
	})
	require.NoError(t, err)

	// Check if the commit was successful by running the 'git log' command
	cmd = exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	require.NoError(t, err)

	// Check if the commit message is correct
	if strings.TrimSpace(string(output)) != commitMessage {
		t.Fatalf("expected commit message '%s', got '%s'", commitMessage, output)
	}
}
func TestGitCommitActivity_NoChangesToCommit(t *testing.T) {
	t.Parallel()
	// Create a temporary git repository
	repoDir, cleanup := createTempGitRepo(t)
	defer cleanup()

	// Create a context for the activity (doesn't have to be a real temporal one)
	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	// Call the GitCommitActivity function with CommitAll set to true and a commit message
	commitMessage := "Test commit"
	_, err = GitCommitActivity(ctx, envContainer, GitCommitParams{
		CommitMessage: commitMessage,
		CommitAll:     true,
	})

	// Check if the GitCommitActivity function returns an error
	require.Error(t, err)

	// Check if the error message indicates that the git commit command failed
	require.Contains(t, err.Error(), "git commit failed")
}

func createTempGitRepo(t *testing.T) (string, func()) {
	t.Helper()

	tempDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	err := cmd.Run()
	require.NoError(t, err)

	return tempDir, func() {}
}

func TestGetGitUserConfigActivity(t *testing.T) {
	t.Parallel()
	repoDir, cleanup := createTempGitRepo(t)
	defer cleanup()

	// Set up git user config in the test repo
	cmd := exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoDir
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = repoDir
	require.NoError(t, cmd.Run())

	ctx := context.Background()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{
		RepoDir: repoDir,
	})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}

	config, err := GetGitUserConfigActivity(ctx, envContainer)
	require.NoError(t, err)
	require.Equal(t, "Test User", config.Name)
	require.Equal(t, "test@example.com", config.Email)
}
