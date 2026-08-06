package env

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"sidekick/common"

	modal "github.com/modal-labs/libmodal/modal-go"
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

		output, err = modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "rg",
			Args:    []string{"--version"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, output.ExitStatus, "stderr: %s", output.Stderr)
		assert.Contains(t, output.Stdout, "ripgrep")
	})

	t.Run("api fallback when the ssh endpoint is unusable", func(t *testing.T) {
		// Injecting a dead endpoint that survives refresh forces the real
		// libmodal fallback, which otherwise only happens on networks that
		// cannot reach the tunnel port.
		deadListener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		deadPort := deadListener.Addr().(*net.TCPAddr).Port
		require.NoError(t, deadListener.Close())

		fallbackEnv := &ModalEnv{
			SandboxName:      sandboxName,
			SSHHost:          "127.0.0.1",
			SSHPort:          deadPort,
			WorkingDirectory: syncOutput.RemoteRepoDir,
			refreshModalEndpoint: func(context.Context, string) (string, int, error) {
				return "127.0.0.1", deadPort, nil
			},
		}
		output, err := fallbackEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "sh",
			Args:    []string{"-c", "echo hello-from-api; exit 5"},
		})
		require.NoError(t, err)
		assert.Equal(t, "hello-from-api\n", output.Stdout)
		assert.Equal(t, 5, output.ExitStatus, "the fallback must report the command's exit status")
	})

	t.Run("api command matches ssh output", func(t *testing.T) {
		input := EnvRunCommandInput{
			Command: "sh",
			Args:    []string{"-c", "echo to-stdout; echo term-is-${TERM+set}; echo to-stderr >&2; exit 7"},
		}
		sshOutput, err := modalEnv.RunCommand(ctx, input)
		require.NoError(t, err)
		apiOutput, err := modalEnv.runAPICommandInner(ctx, input)
		require.NoError(t, err)

		assert.Equal(t, "to-stdout\nterm-is-\n", apiOutput.Stdout)
		assert.Equal(t, sshOutput.Stdout, apiOutput.Stdout)
		assert.Equal(t, sshOutput.ExitStatus, apiOutput.ExitStatus)
		assert.Equal(t, 7, apiOutput.ExitStatus)
		assert.Contains(t, apiOutput.Stderr, "to-stderr")
		assert.NotContains(t, apiOutput.Stdout, "\x1b", "output must not carry terminal escape sequences")
		assert.NotContains(t, apiOutput.Stderr, "\x1b", "output must not carry terminal escape sequences")
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

	t.Run("reverse port forward", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer listener.Close()
		go func() {
			for {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				_, _ = conn.Write([]byte("hello-from-host\n"))
				_ = conn.Close()
			}
		}()
		hostPort := listener.Addr().(*net.TCPAddr).Port

		fwdEnv := &ModalEnv{
			SandboxName:      sandboxName,
			SSHHost:          createOutput.SSHHost,
			SSHPort:          createOutput.SSHPort,
			WorkingDirectory: syncOutput.RemoteRepoDir,
			PortForwards:     []common.PortForwardConfig{{HostPort: hostPort, ContainerPort: 18080}},
		}
		output, err := fwdEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "python3",
			Args: []string{"-c",
				"import socket; s = socket.create_connection(('127.0.0.1', 18080), 10); print(s.recv(100).decode())"},
		})
		require.NoError(t, err)
		require.Equal(t, 0, output.ExitStatus, "stderr: %s", output.Stderr)
		assert.Contains(t, output.Stdout, "hello-from-host")
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

// TestModalActiveSnapshotIntegration proves the watchdog's periodic
// active-snapshot behavior end to end: a busy sandbox with a short
// active-snapshot interval produces a guard snapshot while remaining alive.
// It requires Modal credentials and consumes Modal compute, so it is gated
// behind SIDE_E2E_TEST.
func TestModalActiveSnapshotIntegration(t *testing.T) {
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

	client, err := getModalClient()
	if err != nil {
		t.Skip("modal client unavailable: " + err.Error())
	}
	if _, err := findModalSandbox(ctx, client, "side-e2e-credential-probe"); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}

	sandboxName := "side-e2e-snap-" + strings.ToLower(ksuid.New().String()[:10])
	// Minimal image and sizing keep this billed sandbox cheap; the sandbox
	// itself only runs sshd plus one sleep. IdleSeconds is high so the idle
	// shutdown cannot interfere and any snapshot must come from the active
	// path.
	createOutput, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{
		Name: sandboxName,
		Config: common.ModalEnvConfig{
			Image:                 "debian:bookworm-slim",
			CPU:                   0.25,
			Memory:                512,
			IdleSeconds:           3600,
			ActiveSnapshotSeconds: 5,
		},
	})
	require.NoError(t, err, "modalCreateSandbox failed")
	t.Cleanup(func() {
		_, _ = DeleteSandboxActivity(context.Background(), DeleteSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
	})

	modalEnv := &ModalEnv{
		SandboxName:      sandboxName,
		SSHHost:          createOutput.SSHHost,
		SSHPort:          createOutput.SSHPort,
		WorkingDirectory: "/root",
	}
	// A long-running command holds an ssh session open, keeping is_busy true
	// across watchdog polls while the guard record is polled below.
	go func() {
		_, _ = modalEnv.RunCommand(ctx, EnvRunCommandInput{
			Command: "sleep",
			Args:    []string{"120"},
		})
	}()

	// The watchdog polls every 15s and snapshots on the first busy poll once
	// the 5s interval has elapsed, so the bound is one poll cycle plus a few
	// seconds of guard latency.
	pollDeadline := time.Now().Add(25 * time.Second)
	var record *modalSnapshotRecord
	for time.Now().Before(pollDeadline) {
		record, err = modalLatestSnapshot(ctx, client, sandboxName)
		require.NoError(t, err)
		if record != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	require.NotNil(t, record, "expected an active snapshot to appear while the sandbox was busy")
	assert.NotEmpty(t, record.ImageId)

	sb, err := findModalSandbox(ctx, client, sandboxName)
	require.NoError(t, err)
	assert.NotNil(t, sb, "sandbox must remain alive after an active snapshot")
}

// TestModalSnapshotVolumeRestoreIntegration covers adding a volume mount to a
// repository whose sandbox already has filesystem snapshots. Snapshots taken
// before the volume existed hold a populated directory at the mount path and
// Modal refuses to mount a volume over it, so such a snapshot must be skipped
// in favour of a clean image. Recreating the sandbox the moment its
// termination begins is part of the same sequence, covering the window where
// a dying sandbox still polls as running. It requires Modal credentials and
// consumes Modal compute, so it is gated behind SIDE_E2E_TEST.
func TestModalSnapshotVolumeRestoreIntegration(t *testing.T) {
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

	client, err := getModalClient()
	if err != nil {
		t.Skip("modal client unavailable: " + err.Error())
	}
	if _, err := findModalSandbox(ctx, client, "side-e2e-credential-probe"); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}

	suffix := strings.ToLower(ksuid.New().String()[:10])
	sandboxName := "side-e2e-snap-" + suffix
	volumeName := "side-e2e-snap-vol-" + suffix
	const mountPath = "/root/sidekick-e2e-cache"
	// Minimal image and sizing keep this billed sandbox cheap; a high idle
	// timeout keeps the watchdog out of the way so the shutdown below is the
	// only one in play.
	config := common.ModalEnvConfig{
		Image:       "debian:bookworm-slim",
		CPU:         0.25,
		Memory:      512,
		IdleSeconds: 3600,
	}
	createOutput, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: sandboxName, Config: config})
	require.NoError(t, err, "initial modalCreateSandbox failed")
	t.Cleanup(func() {
		_, _ = DeleteSandboxActivity(context.Background(), DeleteSandboxInput{EnvType: EnvTypeModal, SandboxName: sandboxName})
		// Modal only releases a volume once the sandbox mounting it is gone,
		// so deletion has to wait out the sandbox's teardown.
		var err error
		for attempt := 0; attempt < 10; attempt++ {
			err = client.Volumes.Delete(context.Background(), volumeName, nil)
			var notFound modal.NotFoundError
			if err == nil || errors.As(err, &notFound) {
				return
			}
			time.Sleep(3 * time.Second)
		}
		t.Logf("failed to delete test volume %s: %v", volumeName, err)
	})

	modalEnv := &ModalEnv{
		SandboxName:      sandboxName,
		SSHHost:          createOutput.SSHHost,
		SSHPort:          createOutput.SSHPort,
		WorkingDirectory: "/root",
	}
	// Without a volume the future mount path is an ordinary directory, and
	// its contents land in the snapshot.
	writeOutput, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", "mkdir -p " + mountPath + " && printf legacy > " + mountPath + "/value"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, writeOutput.ExitStatus, "stderr: %s", writeOutput.Stderr)

	// The guard's configuration lives in the sandbox process environment, which
	// only Modal exec inherits, so request the snapshot the way the in-sandbox
	// watchdog does rather than over SSH.
	sb, err := findModalSandbox(ctx, client, sandboxName)
	require.NoError(t, err)
	require.NotNil(t, sb)
	snapshotStdout, snapshotStderr, snapshotExit, err := modalExecCapture(ctx, sb, "/usr/local/bin/sidekick-snapshot snapshot")
	require.NoError(t, err)
	require.Equal(t, 0, snapshotExit, "guard snapshot failed: %s%s", snapshotStdout, snapshotStderr)

	var record *modalSnapshotRecord
	snapshotDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(snapshotDeadline) {
		record, err = modalLatestSnapshot(ctx, client, sandboxName)
		require.NoError(t, err)
		if record != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	require.NotNil(t, record, "the guard must record a snapshot, otherwise the restore path is never exercised")
	require.Equal(t, modalSnapshotImageVersion, record.ImageVersion)

	// Terminate without waiting, like the guard's terminate phase, then
	// immediately recreate. During this window the old sandbox can still poll
	// as running while refusing exec with FailedPrecondition.
	_, err = sb.Terminate(ctx, nil)
	require.NoError(t, err)

	config.Volumes = []common.ModalVolumeMount{{Name: volumeName, MountPath: mountPath}}

	createOutput, err = modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: sandboxName, Config: config})
	require.NoError(t, err, "modalCreateSandbox must recover when the newest snapshot predates the volume")
	require.NotEmpty(t, createOutput.SSHHost)
	require.NotZero(t, createOutput.SSHPort)

	modalEnv.SSHHost = createOutput.SSHHost
	modalEnv.SSHPort = createOutput.SSHPort
	runOutput, err := modalEnv.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", "printf fresh > " + mountPath + "/value && cat " + mountPath + "/value"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, runOutput.ExitStatus, "stderr: %s", runOutput.Stderr)
	assert.Equal(t, "fresh", strings.TrimSpace(runOutput.Stdout))
}
