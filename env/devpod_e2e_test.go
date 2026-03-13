package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

func TestDevPodIntegration(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("skipping DevPod integration test; SIDE_INTEGRATION_TEST not set to true")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	ctx := context.Background()
	workspacePath := repoRoot(t)

	// Start the DevPod workspace from the repo's .devcontainer config.
	err := DevPodUpActivity(ctx, DevPodUpInput{WorkspacePath: workspacePath})
	require.NoError(t, err, "DevPodUpActivity failed")

	t.Cleanup(func() {
		if err := DevPodDeleteActivity(context.Background(), workspacePath); err != nil {
			t.Logf("DevPodDeleteActivity cleanup error: %v", err)
		}
	})

	// Inside the container the workspace is mounted at /workspaces/<basename>
	// per the devcontainer spec default.
	containerWorkDir := "/workspaces/" + filepath.Base(workspacePath)

	devEnv := &DevPodEnv{
		WorkingDirectory: containerWorkDir,
		WorkspaceName:    workspacePath,
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

	t.Run("go unit tests inside container", func(t *testing.T) {
		out, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "go",
			Args:    []string{"test", "./common/..."},
		})
		require.NoError(t, err)
		t.Logf("stdout:\n%s", out.Stdout)
		t.Logf("stderr:\n%s", out.Stderr)
		assert.Equal(t, 0, out.ExitStatus, "go test ./common/... failed")
	})

	t.Run("frontend tests inside container", func(t *testing.T) {
		out, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			RelativeWorkingDir: "frontend",
			Command:            "bun",
			Args:               []string{"run", "test:unit", "--run"},
			EnvVars:            []string{"PATH=/home/vscode/.bun/bin:/usr/local/bin:/usr/bin:/bin"},
		})
		require.NoError(t, err)
		t.Logf("stdout:\n%s", out.Stdout)
		t.Logf("stderr:\n%s", out.Stderr)
		assert.Equal(t, 0, out.ExitStatus, "frontend tests failed")
	})

	t.Run("create worktree inside container", func(t *testing.T) {
		// Create a fresh git repo inside the container so that .git is a
		// real directory (not a worktree reference to a host path).
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
			WorkspaceName:    workspacePath,
		}
		repoEnvContainer := EnvContainer{Env: repoEnv}

		wsId := "ws-" + ksuid.New().String()
		branchName := "side/devpod-e2e-test-" + ksuid.New().String()

		output, err := CreateDevPodWorktreeActivity(ctx, CreateDevPodWorktreeInput{
			EnvContainer: repoEnvContainer,
			RepoDir:      containerRepoDir,
			BranchName:   branchName,
			WorkspaceId:  wsId,
		})
		require.NoError(t, err, "CreateDevPodWorktreeActivity failed")
		assert.NotEmpty(t, output.WorktreePath)
		t.Logf("worktree created at: %s", output.WorktreePath)

		// Verify the worktree directory exists inside the container.
		verifyOut, err := devEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "test",
			Args:    []string{"-d", output.WorktreePath},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, verifyOut.ExitStatus, "worktree directory does not exist in container")
	})
}