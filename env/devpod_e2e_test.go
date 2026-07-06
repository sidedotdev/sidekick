package env

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
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

// minimalDockerfileHash returns a short content hash of the minimal devcontainer
// Dockerfile fixture, used to key the cached workspace so repeated runs reuse the
// previously built image instead of rebuilding.
func minimalDockerfileHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// setupMinimalWorkspace materializes the devcontainer workspace at a stable path
// keyed by the fixture Dockerfile content hash (rather than a per-run temp dir),
// so devpod reuses the previously built image. It is idempotent: an already
// materialized workspace is reused as-is.
func setupMinimalWorkspace(t *testing.T, hash string) string {
	t.Helper()
	dockerfile, err := os.ReadFile(filepath.Join(repoRoot(t), "env", "testdata", "Dockerfile.minimal"))
	require.NoError(t, err)

	dir := filepath.Join(os.TempDir(), "sidekick-e2e-devpod-"+hash)
	if _, err := os.Stat(filepath.Join(dir, ".devcontainer", "devcontainer.json")); err == nil {
		return dir
	}

	devcontainerDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "Dockerfile"), dockerfile, 0644))

	devcontainerJSON := `{"name": "test", "build": {"dockerfile": "Dockerfile"}}`
	require.NoError(t, os.WriteFile(filepath.Join(devcontainerDir, "devcontainer.json"), []byte(devcontainerJSON), 0644))

	// Initialize a git repository so the synced container working directory is
	// git-backed, which Env.Walk requires.
	for _, args := range [][]string{
		{"init"},
		{"checkout", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@test.com"},
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}

	return dir
}

func TestDevPodIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping DevPod integration test; SIDE_E2E_TEST not set to true")
	}
	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where containers can be created.
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping DevPod integration test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("devpod"); err != nil {
		t.Skip("devpod command not found in PATH")
	}

	// Key the cached workspace + built image by the fixture Dockerfile hash so
	// repeated runs reuse them. The workspace is intentionally not deleted on
	// cleanup so it persists for reuse.
	hash := minimalDockerfileHash(t)
	workspacePath := setupMinimalWorkspace(t, hash)
	workspaceName := "side-e2e-devpod-" + hash

	// Start the DevPod workspace. When the container is already running, this
	// returns quickly.
	err := DevPodUpActivity(context.Background(), DevPodUpInput{WorkspacePath: workspacePath, IDE: "none", WorkspaceId: workspaceName})
	require.NoError(t, err, "DevPodUpActivity failed")

	// Derive a context that leaves time for cleanup before the test deadline.
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	containerWorkDir := "/workspaces/" + workspaceName

	devEnv := &DevPodEnv{
		WorkingDirectory: containerWorkDir,
		WorkspaceName:    workspaceName,
		LocalRepoDir:     workspacePath,
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

	t.Run("filesystem methods via sftp", func(t *testing.T) {
		runRemoteEnvFilesystemSubtests(t, ctx, devEnv)
	})

	t.Run("Env interface walk", func(t *testing.T) {
		runRemoteEnvWalkSubtests(t, ctx, devEnv)
	})

	t.Run("stdin detached from SSH stream", func(t *testing.T) {
		runRemoteEnvStdinSubtests(t, ctx, devEnv)
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

		// The workspace container is reused across runs, so remove this per-run
		// repo and its worktree to avoid unbounded writable-layer growth.
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = devEnv.RunCommand(cleanupCtx, EnvRunCommandInput{
				Command: "rm",
				Args:    []string{"-rf", containerRepoDir},
			})
		})

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
