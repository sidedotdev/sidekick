package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot returns the absolute path to the repository root via git.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "failed to determine repo root")
	return strings.TrimSpace(string(out))
}

func setupMinimalWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devcontainerDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0755))

	dockerfile, err := os.ReadFile(filepath.Join(repoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "Dockerfile"), dockerfile, 0644))

	devcontainerJSON := `{"name": "test", "build": {"dockerfile": "Dockerfile"}}`
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "devcontainer.json"), []byte(devcontainerJSON), 0644))

	return dir
}

func TestDevPodIntegration(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("skipping DevPod integration test; SIDE_INTEGRATION_TEST not set to true")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	workspacePath := setupMinimalWorkspace(t)

	// Derive a context that leaves time for cleanup before the test deadline.
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	err := DevPodUpActivity(ctx, DevPodUpInput{WorkspacePath: workspacePath})
	require.NoError(t, err, "DevPodUpActivity failed")

	workspaceName := DevPodWorkspaceName(workspacePath)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := DevPodDeleteActivity(cleanupCtx, workspacePath); err != nil {
			t.Logf("DevPodDeleteActivity cleanup error: %v", err)
		}
	})

	containerWorkDir := "/workspaces/" + filepath.Base(workspacePath)

	devEnv := &DevPodEnv{
		WorkingDirectory: containerWorkDir,
		WorkspaceName:    workspaceName,
	}

	t.Run("basic command execution", func(t *testing.T) {
		out, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "echo",
			Args:    []string{"hello from devpod"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, out.ExitStatus)
		assert.Contains(t, out.Stdout, "hello from devpod")
		t.Logf("stdout: %s", out.Stdout)
	})

	t.Run("create worktree inside container", func(t *testing.T) {
		containerRepoDir := "/tmp/devpod-e2e-repo-" + ksuid.New().String()
		initScript := strings.Join([]string{
			"mkdir -p " + containerRepoDir,
			"cd " + containerRepoDir,
			"git init",
			"git checkout -b main",
			"git config user.name 'Test'",
			"git config user.email 'test@test.com'",
			"git commit --allow-empty -m 'init'",
		}, " && ")
		initOut, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "bash",
			Args:    []string{"-c", initScript},
		})
		require.NoError(t, err)
		require.Equal(t, 0, initOut.ExitStatus, "repo init failed: %s", initOut.Stderr)

		repoEnv := &DevPodEnv{
			WorkingDirectory: containerRepoDir,
			WorkspaceName:    workspaceName,
		}
		envContainer := EnvContainer{Env: repoEnv}

		wsId := "ws-" + ksuid.New().String()
		branchName := "side/devpod-e2e-test-" + ksuid.New().String()

		output, err := CreateDevPodWorktreeActivity(ctx, CreateDevPodWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      containerRepoDir,
			BranchName:   branchName,
			WorkspaceId:  wsId,
		})
		require.NoError(t, err, "CreateDevPodWorktreeActivity failed")
		assert.NotEmpty(t, output.WorktreePath)
		t.Logf("worktree created at: %s", output.WorktreePath)

		verifyOut, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "test",
			Args:    []string{"-d", output.WorktreePath},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, verifyOut.ExitStatus, "worktree directory does not exist in container")
	})
}
