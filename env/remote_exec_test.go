package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/sideagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFakeSSHInvocations returns one line per fake-ssh invocation logged to
// logPath.
func readFakeSSHInvocations(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestAgentExecDialInstallOnlyWhenAgentAbsent verifies the
// connect-attempt-as-check bootstrap: only a "command not found" channel
// failure (exit 127, ie the agent binary is missing) triggers an install
// attempt; other transport failures do not.
func TestAgentExecDialInstallOnlyWhenAgentAbsent(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "invocations")
	exitCodePath := filepath.Join(tempDir, "exit-code")
	fakeSSH := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %s
exit "$(cat %s)"
`, shellQuote(logPath), shellQuote(exitCodePath))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "ssh"), []byte(fakeSSH), 0700))
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A non-127 channel failure is transport-level: no install attempt.
	require.NoError(t, os.WriteFile(exitCodePath, []byte("1\n"), 0644))
	err := (&agentExecConn{}).dialWithArgsLocked(context.Background(), []string{"remote-host"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "install agent binary")
	require.Len(t, readFakeSSHInvocations(t, logPath), 1)

	// Exit 127 means the agent binary is absent: an install session (whose
	// megacommand starts with uname) must follow. The fake ssh fails that
	// session too, which the dial reports as an install failure.
	require.NoError(t, os.WriteFile(logPath, nil, 0644))
	require.NoError(t, os.WriteFile(exitCodePath, []byte("127\n"), 0644))
	err = (&agentExecConn{}).dialWithArgsLocked(context.Background(), []string{"remote-host"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "install agent binary")
	invocations := readFakeSSHInvocations(t, logPath)
	require.Len(t, invocations, 2)
	assert.Contains(t, invocations[1], "uname -sm && ")
}

// TestAgentExecDialClassifiesSilentSSHTransportFailure covers OpenSSH's
// empty-stderr exit-255 mode (e.g. the peer drops the connection before the
// banner exchange under LogLevel=ERROR): the dial must still surface a typed
// pre-protocol transport failure that the Modal classifier recognizes.
func TestAgentExecDialClassifiesSilentSSHTransportFailure(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	tempDir := t.TempDir()
	fakeSSH := "#!/bin/sh\nexit 255\n"
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "ssh"), []byte(fakeSSH), 0700))
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := (&agentExecConn{}).dialWithArgsLocked(context.Background(), []string{"remote-host"})
	require.Error(t, err)
	var dialErr *sshDialTransportError
	require.ErrorAs(t, err, &dialErr)
	assert.True(t, isModalSSHTransportFailure(err.Error()),
		"the classifier must recognize the typed dial failure without ssh stderr text")
}

// startTestAgentClient wires a sideagent.Client to an in-process Serve over
// pipes, standing in for the ssh-hosted channel.
func startTestAgentClient(t *testing.T) *sideagent.Client {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	go func() {
		_ = sideagent.Serve(reqR, respW)
		respW.Close()
	}()
	t.Cleanup(func() { _ = reqW.Close() })
	return sideagent.NewClient(respR, reqW)
}

func TestGetPooledAgentExecConn_SharesConnByKey(t *testing.T) {
	t.Parallel()
	a := getPooledAgentExecConn("test-agent-share-a")
	b := getPooledAgentExecConn("test-agent-share-a")
	c := getPooledAgentExecConn("test-agent-share-b")
	assert.Same(t, a, b, "same key must share one conn")
	assert.NotSame(t, a, c, "different keys must not share")
	a.Close()
	c.Close()
}

func TestAgentExecConnKeyIncludesNormalizedPortForwards(t *testing.T) {
	t.Parallel()

	base := "modal:test-agent-forward-key"
	withoutForwards := agentExecConnKey(base, nil)
	withForwards := agentExecConnKey(base, []common.PortForwardConfig{
		{HostPort: 8080},
		{HostPort: 18855, ContainerPort: 28855},
	})
	reordered := agentExecConnKey(base, []common.PortForwardConfig{
		{HostPort: 18855, ContainerPort: 28855},
		{HostPort: 8080, ContainerPort: 8080},
	})

	assert.NotEqual(t, withoutForwards, withForwards)
	assert.Equal(t, withForwards, reordered)
	withoutConn := getPooledAgentExecConn(withoutForwards)
	withConn := getPooledAgentExecConn(withForwards)
	assert.NotSame(t, withoutConn, withConn, "different forwarding configurations need distinct SSH channels")
	withoutConn.Close()
	withConn.Close()
}

func TestAgentExecConn_CloseReapsProcessAndPool(t *testing.T) {
	t.Parallel()
	conn := getPooledAgentExecConn("test-agent-close")
	cmd := startSleepProcess(t)
	conn.mu.Lock()
	conn.client = startTestAgentClient(t)
	conn.cmd = cmd
	conn.mu.Unlock()

	conn.Close()

	require.Eventually(t, func() bool {
		return cmd.ProcessState != nil
	}, 5*time.Second, 10*time.Millisecond, "ssh stand-in process should be reaped")

	replacement := getPooledAgentExecConn("test-agent-close")
	assert.NotSame(t, conn, replacement, "closed conn must be evicted from the pool")
	replacement.Close()
}

func TestReapIdleAgentExecConns(t *testing.T) {
	t.Parallel()

	idle := getPooledAgentExecConn("test-agent-reap-idle")
	idle.mu.Lock()
	idle.lastUsed = time.Now().Add(-2 * time.Hour)
	idle.mu.Unlock()

	fresh := getPooledAgentExecConn("test-agent-reap-fresh")

	busy := getPooledAgentExecConn("test-agent-reap-busy")
	busy.mu.Lock()
	busy.lastUsed = time.Now().Add(-2 * time.Hour)
	busy.inFlight = 1
	busy.mu.Unlock()

	reapIdleAgentExecConns(time.Now().Add(-time.Hour))

	assert.NotSame(t, idle, getPooledAgentExecConn("test-agent-reap-idle"),
		"idle conn should be evicted")
	assert.Same(t, fresh, getPooledAgentExecConn("test-agent-reap-fresh"),
		"fresh conn should survive")
	assert.Same(t, busy, getPooledAgentExecConn("test-agent-reap-busy"),
		"conn with in-flight command should survive despite stale lastUsed")

	busy.mu.Lock()
	busy.inFlight = 0
	busy.mu.Unlock()
	getPooledAgentExecConn("test-agent-reap-idle").Close()
	fresh.Close()
	busy.Close()
}

func TestCloseAllSharedAgentExecConns(t *testing.T) {
	conn := getPooledAgentExecConn("test-agent-close-all")
	cmd := startSleepProcess(t)
	conn.mu.Lock()
	conn.cmd = cmd
	conn.mu.Unlock()

	CloseAllSharedAgentExecConns()

	require.Eventually(t, func() bool {
		return cmd.ProcessState != nil
	}, 5*time.Second, 10*time.Millisecond)
	assert.NotSame(t, conn, getPooledAgentExecConn("test-agent-close-all"))
	getPooledAgentExecConn("test-agent-close-all").Close()
}

// TestRunAgentExec_LockLiveFollowsReplacement verifies an evicted conn's
// lockLive follows the pool to the live replacement entry.
func TestAgentExecConn_LockLiveFollowsReplacement(t *testing.T) {
	t.Parallel()
	conn := getPooledAgentExecConn("test-agent-lock-live")
	conn.Close()
	live := conn.lockLive()
	followedToReplacement := conn != live
	evicted := live.evicted
	live.mu.Unlock()
	assert.True(t, followedToReplacement)
	assert.False(t, evicted)
	live.Close()
}

func TestAgentExecRequest(t *testing.T) {
	t.Parallel()

	forwards := []common.PortForwardConfig{{HostPort: 18855, ContainerPort: 28855}}
	req := agentExecRequest("/work/tree", EnvTypeOpenShell, forwards, EnvRunCommandInput{
		RelativeWorkingDir: "sub/dir",
		Command:            "rg",
		Args:               []string{"-n", "some pattern with spaces", "."},
		EnvVars:            []string{"FOO=bar"},
	})

	assert.Equal(t, "/work/tree/sub/dir", req.Dir)
	assert.Equal(t, []string{"rg", "-n", "some pattern with spaces", "."}, req.Argv)
	assert.Contains(t, req.Env, "FOO=bar")
	assert.Contains(t, req.Env, "SIDE_PORT_FORWARDS=18855:28855")
	assert.Contains(t, req.Env, "GIT_EDITOR=true")
	assert.Contains(t, req.Env, "SIDE_ACTIVE_ENV_TYPE=openshell")

	noForwards := agentExecRequest("/work/tree", EnvTypeOpenShell, nil, EnvRunCommandInput{Command: "true"})
	for _, entry := range noForwards.Env {
		assert.NotContains(t, entry, "SIDE_PORT_FORWARDS")
	}
}

func TestWithRemoteReadLock(t *testing.T) {
	t.Parallel()
	req := withRemoteReadLock(sideagent.ExecRequest{Argv: []string{"true"}}, "/work/tree")
	assert.Equal(t, hibernationLockFile("/work/tree"), req.ReadLockFile)
	assert.Equal(t, "/work/tree/"+HibernationMetadataFile, req.HibernationSentinel)
}

func TestAgentExecOutput(t *testing.T) {
	t.Parallel()

	t.Run("plain success", func(t *testing.T) {
		out := agentExecOutput(sideagent.ExecResponse{Stdout: []byte("ok\n"), ExitStatus: 0})
		assert.Equal(t, "ok\n", out.Stdout)
		assert.Equal(t, 0, out.ExitStatus)
	})

	t.Run("hibernated maps to sentinel exit code", func(t *testing.T) {
		out := agentExecOutput(sideagent.ExecResponse{Hibernated: true})
		assert.Equal(t, hibernatedRemoteExitCode, out.ExitStatus)
	})

	t.Run("start failure surfaces on stderr with non-zero exit", func(t *testing.T) {
		out := agentExecOutput(sideagent.ExecResponse{ExitStatus: -1, Error: "exec: no such file"})
		assert.NotEqual(t, 0, out.ExitStatus)
		assert.Contains(t, out.Stderr, "exec: no such file")
	})
}

func TestChannelSSHArgs(t *testing.T) {
	t.Parallel()
	in := []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/sock",
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-R", "127.0.0.1:8855:127.0.0.1:8855",
		"host",
	}
	out := channelSSHArgs(in)
	assert.Equal(t, []string{
		"-o", "BatchMode=yes",
		"-R", "127.0.0.1:8855:127.0.0.1:8855",
		"host",
	}, out, "channel args must drop the multiplexing master but keep reverse forwards")

	indep := independentSSHArgs(in)
	assert.NotContains(t, indep, "-R", "independent args must also drop reverse forwards")
}

// TestRunAgentExec_ContextCancellation verifies a canceled context returns
// promptly without tearing down the (still healthy) channel.
func TestRunAgentExec_ContextCancellation(t *testing.T) {
	t.Parallel()
	conn := getPooledAgentExecConn("test-agent-ctx-cancel")
	client := startTestAgentClient(t)
	conn.mu.Lock()
	conn.client = client
	conn.mu.Unlock()
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := runAgentExec(ctx, "test-agent-ctx-cancel", nil, sideagent.ExecRequest{Argv: []string{"sleep", "30"}})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	conn.mu.Lock()
	stillConnected := conn.client == client
	conn.mu.Unlock()
	assert.True(t, stillConnected, "healthy channel must survive a canceled command")
}
func TestSynchronizedBufferConcurrentAccess(t *testing.T) {
	t.Parallel()

	var diagnostics synchronizedBuffer
	const writers = 8
	const writesPerWriter = 100
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				_, err := diagnostics.Write([]byte("diagnostic\n"))
				assert.NoError(t, err)
				_ = diagnostics.String()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, writers*writesPerWriter*len("diagnostic\n"), len(diagnostics.String()))
}

func TestAgentExecConnDiagnosticsBelongToExactClient(t *testing.T) {
	t.Parallel()

	conn := getPooledAgentExecConn("test-agent-diagnostics")
	client := startTestAgentClient(t)
	diagnostics := &synchronizedBuffer{}
	_, err := diagnostics.Write([]byte("ssh: connection reset"))
	require.NoError(t, err)
	conn.mu.Lock()
	conn.client = client
	conn.diagnostics = diagnostics
	conn.mu.Unlock()
	defer conn.Close()

	assert.Equal(t, "ssh: connection reset", conn.diagnosticsFor(client))
	assert.Empty(t, conn.diagnosticsFor(startTestAgentClient(t)))
}
