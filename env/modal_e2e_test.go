package env

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
	createOutput, err := CreateSandboxActivity(ctx, CreateSandboxInput{EnvType: EnvTypeModal, Name: sandboxName})
	require.NoError(t, err, "CreateSandboxActivity failed")
	require.NotEmpty(t, createOutput.SSHHost)
	require.NotZero(t, createOutput.SSHPort)
	t.Cleanup(func() {
		_, _ = DeleteSandboxActivity(context.Background(), DeleteSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
	})

	localRepoDir := setupMinimalGitRepo(t)
	// Enough commits to exceed the seed clone depth, so the sandbox repo
	// starts shallow and the deepen path has something to do.
	for i := 0; i < seedCloneDepth+5; i++ {
		cmd := exec.Command("git", "commit", "--allow-empty", "-m", fmt.Sprintf("filler %d", i))
		cmd.Dir = localRepoDir
		commitOut, commitErr := cmd.CombinedOutput()
		require.NoError(t, commitErr, "git commit failed: %s", commitOut)
	}
	modalEnv := &ModalEnv{
		SandboxName:  sandboxName,
		SSHHost:      createOutput.SSHHost,
		SSHPort:      createOutput.SSHPort,
		LocalRepoDir: localRepoDir,
	}
	syncOutput, err := SyncRepoToRemoteActivity(ctx, SyncRepoToRemoteInput{
		EnvContainer: EnvContainer{Env: modalEnv},
		LocalRepoDir: localRepoDir,
	})
	require.NoError(t, err, "SyncRepoToRemoteActivity failed")
	require.NotEmpty(t, syncOutput.RemoteRepoDir)
	modalEnv.WorkingDirectory = syncOutput.RemoteRepoDir

	t.Run("run command", func(t *testing.T) {
		output, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"log", "--oneline"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, output.ExitStatus, "stderr: %s", output.Stderr)
		// The seed clone is shallow, so only recent commits are present.
		assert.Contains(t, output.Stdout, "filler")
		assert.NotContains(t, output.Stdout, "init")
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
		wtOutput, err := CreateRemoteWorktreeActivity(ctx, CreateRemoteWorktreeInput{
			EnvContainer: EnvContainer{Env: modalEnv},
			RepoDir:      syncOutput.RemoteRepoDir,
			BranchName:   "side/e2e-test",
			WorkspaceId:  "e2e-workspace",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, wtOutput.WorktreePath)
	})

	t.Run("deepen", func(t *testing.T) {
		countOutput, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"rev-list", "--count", "HEAD"},
		})
		require.NoError(t, err)
		require.Equal(t, 0, countOutput.ExitStatus, "stderr: %s", countOutput.Stderr)
		require.Equal(t, strconv.Itoa(seedCloneDepth), strings.TrimSpace(countOutput.Stdout),
			"seeded repo should be shallow at the seed clone depth")

		deepenOutput, err := DeepenRepoActivity(ctx, DeepenRepoInput{
			EnvContainer:  EnvContainer{Env: modalEnv},
			LocalRepoDir:  localRepoDir,
			RemoteRepoDir: syncOutput.RemoteRepoDir,
		})
		require.NoError(t, err)
		assert.True(t, deepenOutput.Deepened)

		countOutput, err = modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"rev-list", "--count", "HEAD"},
		})
		require.NoError(t, err)
		require.Equal(t, 0, countOutput.ExitStatus, "stderr: %s", countOutput.Stderr)
		assert.Equal(t, strconv.Itoa(seedCloneDepth+6), strings.TrimSpace(countOutput.Stdout),
			"deepened repo should have complete history")

		// A second deepen is a no-op.
		deepenOutput, err = DeepenRepoActivity(ctx, DeepenRepoInput{
			EnvContainer:  EnvContainer{Env: modalEnv},
			LocalRepoDir:  localRepoDir,
			RemoteRepoDir: syncOutput.RemoteRepoDir,
		})
		require.NoError(t, err)
		assert.False(t, deepenOutput.Deepened)
	})

	checkOutput, err := CheckSandboxActivity(ctx, CheckSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
	require.NoError(t, err)
	assert.True(t, checkOutput.Alive)

	_, err = DeleteSandboxActivity(ctx, DeleteSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
	require.NoError(t, err)
	checkOutput, err = CheckSandboxActivity(ctx, CheckSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
	require.NoError(t, err)
	assert.False(t, checkOutput.Alive)
}
