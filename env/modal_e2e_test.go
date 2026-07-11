package env

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"sidekick/common"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModalIntegration exercises the full Modal sandbox lifecycle end to end:
// create (sshd behind a Modal tunnel), run commands over ssh, file ops via
// sftp, repo sync via git bundle, remote worktree creation, and termination.
// It requires Modal credentials and consumes Modal compute, so it is gated
// behind SIDE_E2E_TEST.
func TestModalIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal e2e test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal e2e test; credentials are unavailable in non-local sidekick environments")
	}
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	// Client creation succeeds without credentials; missing tokens only
	// surface on the first RPC, so probe with a real lookup before running.
	client, err := getModalClient()
	if err != nil {
		t.Skip("modal client unavailable: " + err.Error())
	}
	if _, err := findModalSandbox(ctx, client, "side-e2e-credential-probe"); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}

	sandboxName := "side-e2e-modal-" + strings.ToLower(ksuid.New().String()[:10])
	createOutput, err := ModalCreateSandboxActivity(ctx, ModalCreateSandboxInput{Name: sandboxName})
	require.NoError(t, err, "ModalCreateSandboxActivity failed")
	require.NotEmpty(t, createOutput.SSHHost)
	require.NotZero(t, createOutput.SSHPort)
	t.Cleanup(func() {
		_, _ = ModalDeleteActivity(context.Background(), ModalDeleteInput{SandboxName: sandboxName})
	})

	localRepoDir := setupMinimalGitRepo(t)
	syncOutput, err := ModalSyncRepoActivity(ctx, ModalSyncRepoInput{
		SandboxName:  sandboxName,
		SSHHost:      createOutput.SSHHost,
		SSHPort:      createOutput.SSHPort,
		LocalRepoDir: localRepoDir,
	})
	require.NoError(t, err, "ModalSyncRepoActivity failed")
	require.NotEmpty(t, syncOutput.ContainerRepoDir)

	modalEnv := &ModalEnv{
		WorkingDirectory: syncOutput.ContainerRepoDir,
		SandboxName:      sandboxName,
		SSHHost:          createOutput.SSHHost,
		SSHPort:          createOutput.SSHPort,
		LocalRepoDir:     localRepoDir,
	}

	t.Run("run command", func(t *testing.T) {
		output, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"log", "--oneline"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, output.ExitStatus, "stderr: %s", output.Stderr)
		assert.Contains(t, output.Stdout, "init")
	})

	t.Run("file operations", func(t *testing.T) {
		content := []byte("hello from modal\n")
		require.NoError(t, modalEnv.WriteFile(ctx, "modal-test.txt", content, 0o644))
		read, err := modalEnv.ReadFile(ctx, "modal-test.txt")
		require.NoError(t, err)
		assert.Equal(t, content, read)
		info, err := modalEnv.Stat(ctx, "modal-test.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(len(content)), info.Size())
		require.NoError(t, modalEnv.Remove(ctx, "modal-test.txt"))
	})

	t.Run("worktree", func(t *testing.T) {
		wtOutput, err := CreateModalWorktreeActivity(ctx, CreateModalWorktreeInput{
			EnvContainer: EnvContainer{Env: modalEnv},
			RepoDir:      syncOutput.ContainerRepoDir,
			BranchName:   "side/e2e-test",
			WorkspaceId:  "e2e-workspace",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, wtOutput.WorktreePath)
	})

	checkOutput, err := ModalCheckSandboxActivity(ctx, ModalCheckSandboxInput{SandboxName: sandboxName})
	require.NoError(t, err)
	assert.True(t, checkOutput.Alive)

	_, err = ModalDeleteActivity(ctx, ModalDeleteInput{SandboxName: sandboxName})
	require.NoError(t, err)
	checkOutput, err = ModalCheckSandboxActivity(ctx, ModalCheckSandboxInput{SandboxName: sandboxName})
	require.NoError(t, err)
	assert.False(t, checkOutput.Alive)
}
