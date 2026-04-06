package env

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMinimalGitRepo creates a small local git repo for sync testing.
func setupMinimalGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"checkout", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@test.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}
	return dir
}

func TestOpenShellIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping OpenShell e2e test; SIDE_E2E_TEST not set to true")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell command not found in PATH")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	// Use the pre-cached base community image so sandbox creation is fast.
	createOutput, err := OpenShellCreateActivity(ctx, OpenShellCreateInput{
		Source: "base",
	})
	require.NoError(t, err, "OpenShellCreateActivity failed")
	require.NotEmpty(t, createOutput.SandboxName)
	t.Logf("sandbox created: %s", createOutput.SandboxName)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := OpenShellStopActivity(cleanupCtx, createOutput.SandboxName); err != nil {
			t.Logf("OpenShellStopActivity cleanup error: %v", err)
		}
	})

	osEnv := &OpenShellEnv{
		WorkingDirectory: "/tmp",
		SandboxName:      createOutput.SandboxName,
	}

	t.Run("basic command execution", func(t *testing.T) {
		out, err := osEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "echo",
			Args:    []string{"hello from openshell"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, out.ExitStatus)
		assert.Contains(t, out.Stdout, "hello from openshell")
		t.Logf("stdout: %s", out.Stdout)
	})

	t.Run("sync and verify repo", func(t *testing.T) {
		localRepo := setupMinimalGitRepo(t)

		syncOutput, err := OpenShellSyncRepoActivity(ctx, OpenShellSyncRepoInput{
			SandboxName:  createOutput.SandboxName,
			LocalRepoDir: localRepo,
		})
		require.NoError(t, err, "OpenShellSyncRepoActivity failed")
		assert.NotEmpty(t, syncOutput.ContainerRepoDir)

		repoEnv := &OpenShellEnv{
			WorkingDirectory: syncOutput.ContainerRepoDir,
			SandboxName:      createOutput.SandboxName,
		}
		out, err := repoEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"log", "--oneline", "-1"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, out.ExitStatus, "git log failed: %s", out.Stderr)
		assert.Contains(t, out.Stdout, "init")
		t.Logf("latest commit: %s", strings.TrimSpace(out.Stdout))
	})

	t.Run("create worktree inside sandbox", func(t *testing.T) {
		containerRepoDir := "/tmp/openshell-e2e-repo-" + ksuid.New().String()
		initScript := strings.Join([]string{
			"mkdir -p " + containerRepoDir,
			"cd " + containerRepoDir,
			"git init",
			"git checkout -b main",
			"git config user.name 'Test'",
			"git config user.email 'test@test.com'",
			"git commit --allow-empty -m 'init'",
		}, " && ")
		initOut, err := osEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "bash",
			Args:    []string{"-c", initScript},
		})
		require.NoError(t, err)
		require.Equal(t, 0, initOut.ExitStatus, "repo init failed: %s", initOut.Stderr)

		repoEnv := &OpenShellEnv{
			WorkingDirectory: containerRepoDir,
			SandboxName:      createOutput.SandboxName,
		}
		envContainer := EnvContainer{Env: repoEnv}

		wsId := "ws-" + ksuid.New().String()
		branchName := "side/openshell-e2e-test-" + ksuid.New().String()

		output, err := CreateOpenShellWorktreeActivity(ctx, CreateOpenShellWorktreeInput{
			EnvContainer: envContainer,
			RepoDir:      containerRepoDir,
			BranchName:   branchName,
			WorkspaceId:  wsId,
		})
		require.NoError(t, err, "CreateOpenShellWorktreeActivity failed")
		assert.NotEmpty(t, output.WorktreePath)
		t.Logf("worktree created at: %s", output.WorktreePath)

		verifyOut, err := osEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "test",
			Args:    []string{"-d", output.WorktreePath},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, verifyOut.ExitStatus, "worktree directory does not exist in sandbox")
	})
}
