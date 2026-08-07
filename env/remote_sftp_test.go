package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSleepProcess launches a cheap long-running local process (standing in
// for the remote ssh+agent chain) whose reaping can be observed in tests.
func startSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	return cmd
}

// processAlive reports whether p is still running. After cmd.Wait reaps the
// process, signalling it fails, which we treat as no longer alive.
func processAlive(p *os.Process) bool {
	return p.Signal(syscall.Signal(0)) == nil
}

func TestIndependentSSHArgs(t *testing.T) {
	t.Parallel()

	in := []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/devpod-ssh-myapp",
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"-R", "127.0.0.1:18855:127.0.0.1:18855",
		"some-host.devpod",
		"--",
	}

	out := independentSSHArgs(in)
	joined := strings.Join(out, " ")

	assert.NotContains(t, joined, "ControlMaster")
	assert.NotContains(t, joined, "ControlPersist")
	assert.NotContains(t, joined, "ControlPath")
	assert.NotContains(t, joined, "-S")
	assert.NotContains(t, joined, "/tmp/devpod-ssh-myapp")
	assert.NotContains(t, joined, "-R")
	assert.NotContains(t, joined, "18855")

	// Non-multiplexing options and the destination must be preserved in order.
	assert.Equal(t, []string{
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"some-host.devpod",
		"--",
	}, out)

	// Input must not be mutated.
	assert.Contains(t, in, "ControlMaster=auto")
}

func TestGetPooledSFTPConn_SharesConnByKey(t *testing.T) {
	env1 := &DevPodEnv{WorkspaceName: "ws-shared"}
	env2 := &DevPodEnv{WorkspaceName: "ws-shared"}
	env3 := &DevPodEnv{WorkspaceName: "ws-other"}

	conn1 := getPooledSFTPConn(env1.sftpConnKey())
	conn2 := getPooledSFTPConn(env2.sftpConnKey())
	conn3 := getPooledSFTPConn(env3.sftpConnKey())

	if conn1 != conn2 {
		t.Errorf("expected envs sharing a key to obtain the same pooled conn, got %p and %p", conn1, conn2)
	}
	if conn1 == conn3 {
		t.Errorf("expected envs with different keys to obtain distinct pooled conns, both were %p", conn1)
	}

	osEnv := &OpenShellEnv{SandboxName: "ws-shared"}
	if got := getPooledSFTPConn(osEnv.sftpConnKey()); got == conn1 {
		t.Errorf("expected openshell and devpod keys to be distinct even with the same name")
	}
}

func TestSftpConn_CloseReapsProcessAndPool(t *testing.T) {
	const key = "test-close-key"
	cmd := startSleepProcess(t)
	proc := cmd.Process

	sc := getPooledSFTPConn(key)
	sc.mu.Lock()
	sc.cmd = cmd
	sc.mu.Unlock()

	if !processAlive(proc) {
		t.Fatalf("expected process to be alive before Close")
	}

	sc.Close()

	if processAlive(proc) {
		t.Errorf("expected process to be killed after Close")
	}

	sftpPool.mu.Lock()
	_, stillPooled := sftpPool.conns[key]
	sftpPool.mu.Unlock()
	if stillPooled {
		t.Errorf("expected pool entry for %q to be removed after Close", key)
	}
}

func TestSftpConn_IdleTimerReapsProcessAndPool(t *testing.T) {
	const key = "test-idle-key"

	prev := sftpIdleTimeout
	sftpIdleTimeout = 5 * time.Millisecond
	defer func() { sftpIdleTimeout = prev }()

	cmd := startSleepProcess(t)
	proc := cmd.Process

	sc := getPooledSFTPConn(key)
	sc.mu.Lock()
	sc.cmd = cmd
	sc.resetIdleTimerLocked()
	sc.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(proc) {
		if time.Now().After(deadline) {
			t.Fatalf("expected idle timer to kill process")
		}
		time.Sleep(5 * time.Millisecond)
	}

	sftpPool.mu.Lock()
	_, stillPooled := sftpPool.conns[key]
	sftpPool.mu.Unlock()
	if stillPooled {
		t.Errorf("expected pool entry for %q to be removed after idle eviction", key)
	}
}

func TestSharedSFTPConn_ReuseAcrossDeserializedEnvs(t *testing.T) {
	t.Parallel()

	original := &DevPodEnv{
		WorkingDirectory: "/workspaces/sftp-reuse-test",
		WorkspaceName:    "sftp-reuse-test",
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var copy1, copy2 DevPodEnv
	require.NoError(t, json.Unmarshal(data, &copy1))
	require.NoError(t, json.Unmarshal(data, &copy2))

	assert.Same(t, copy1.sharedSFTP(), copy2.sharedSFTP(),
		"deserialized copies of the same env must share one SFTP connection")
	assert.Same(t, original.sharedSFTP(), copy1.sharedSFTP())

	other := &DevPodEnv{WorkspaceName: "sftp-reuse-test-other"}
	assert.NotSame(t, original.sharedSFTP(), other.sharedSFTP(),
		"different workspaces must not share a connection")

	openShell := &OpenShellEnv{SandboxName: original.WorkspaceName}
	assert.NotSame(t, original.sharedSFTP(), openShell.sharedSFTP(),
		"identically named envs of different types must not share a connection")
}

func TestReapIdleSFTPConns(t *testing.T) {
	t.Parallel()

	idle := sharedSFTPConnFor("test:reap-idle")
	fresh := sharedSFTPConnFor("test:reap-fresh")
	idle.mu.Lock()
	idle.lastUsed = time.Now().Add(-2 * sftpIdleTimeout)
	idle.mu.Unlock()

	reapIdleSFTPConns(time.Now().Add(-sftpIdleTimeout))

	assert.NotSame(t, idle, sharedSFTPConnFor("test:reap-idle"),
		"idle entry must be evicted and replaced on next lookup")
	assert.Same(t, fresh, sharedSFTPConnFor("test:reap-fresh"),
		"recently used entry must be retained")

	// A caller still holding the evicted entry is routed to the replacement
	// instead of dialing an orphan session no reaper would ever close.
	live := idle.lockLive()
	live.mu.Unlock()
	assert.NotSame(t, idle, live)
	assert.Same(t, sharedSFTPConnFor("test:reap-idle"), live)

	// An entry locked by an in-flight operation must not be evicted, even if
	// its idle timestamp is stale.
	busy := sharedSFTPConnFor("test:reap-busy")
	busy.mu.Lock()
	busy.lastUsed = time.Now().Add(-2 * sftpIdleTimeout)
	reapIdleSFTPConns(time.Now().Add(-sftpIdleTimeout))
	busy.mu.Unlock()
	assert.Same(t, busy, sharedSFTPConnFor("test:reap-busy"))
}

func TestCloseAllSharedSFTPConns(t *testing.T) {
	// Deliberately not parallel: closing every shared connection would race
	// with parallel tests that populate the cache.
	conn := sharedSFTPConnFor("test:close-all")

	CloseAllSharedSFTPConns()

	sharedSFTPConnsMu.Lock()
	_, ok := sharedSFTPConns["test:close-all"]
	sharedSFTPConnsMu.Unlock()
	assert.False(t, ok, "shutdown must evict every cache entry")

	conn.mu.Lock()
	evicted := conn.evicted
	conn.mu.Unlock()
	assert.True(t, evicted, "holders of a closed entry must be redirected to a fresh one")
}
func TestSftpConn_ReconnectAfterStaleFailureReusesReplacement(t *testing.T) {
	t.Parallel()

	failedClient := &sftp.Client{}
	replacementClient := &sftp.Client{}
	sc := &sftpConn{
		key:      "test-stale-reconnect",
		client:   replacementClient,
		lastUsed: time.Now(),
	}
	t.Cleanup(func() {
		sc.mu.Lock()
		if sc.idleTimer != nil {
			sc.idleTimer.Stop()
		}
		sc.mu.Unlock()
	})

	client, err := sc.reconnectAfterFailure(context.Background(), nil, failedClient)

	require.NoError(t, err)
	assert.Same(t, replacementClient, client,
		"a failure from an obsolete client must not replace the current connection")
	assert.Same(t, replacementClient, sc.client)
}

// pipeRWC combines the two half-duplex pipes backing an in-memory SFTP
// server into the io.ReadWriteCloser sftp.NewServer requires.
type pipeRWC struct {
	io.Reader
	io.WriteCloser
}

// newInMemorySFTPClient returns a real sftp.Client wired to an in-process
// server over pipes, so connection teardown paths (Close) work in tests
// without any ssh process chain.
func newInMemorySFTPClient(t *testing.T) *sftp.Client {
	return newPipeSFTPClient(t, true)
}

// newWedgedSFTPClient returns a client whose Close blocks until test cleanup
// (the server never closes its write side), emulating a wedged remote
// transport.
func newWedgedSFTPClient(t *testing.T) *sftp.Client {
	return newPipeSFTPClient(t, false)
}

func newPipeSFTPClient(t *testing.T, propagateServerEOF bool) *sftp.Client {
	t.Helper()
	clientReads, serverWrites := io.Pipe()
	serverReads, clientWrites := io.Pipe()
	server, err := sftp.NewServer(pipeRWC{serverReads, serverWrites})
	require.NoError(t, err)
	go func() {
		_ = server.Serve()
		if propagateServerEOF {
			// Propagate EOF to the client's receive loop, which Client.Close
			// waits on.
			_ = serverWrites.Close()
		}
	}()
	t.Cleanup(func() { _ = server.Close() })
	client, err := sftp.NewClientPipe(clientReads, clientWrites)
	require.NoError(t, err)
	return client
}

func TestBoundedSFTPOp(t *testing.T) {
	// Deliberately not parallel: shortens the global sftpOpTimeout.
	origTimeout := sftpOpTimeout
	sftpOpTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sftpOpTimeout = origTimeout })

	t.Run("completed op returns its result and keeps the connection", func(t *testing.T) {
		client := newInMemorySFTPClient(t)
		sc := &sftpConn{key: "test-bounded-success", client: client, lastUsed: time.Now()}
		got, err := boundedSFTPOp(context.Background(), sc, client, "read", "/tmp/x", func() (string, error) {
			return "ok", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "ok", got)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		assert.Same(t, client, sc.client, "a healthy connection must be kept")
	})

	t.Run("wedged op times out and invalidates the connection", func(t *testing.T) {
		client := newInMemorySFTPClient(t)
		sc := &sftpConn{key: "test-bounded-timeout", client: client, lastUsed: time.Now()}
		unblock := make(chan struct{})
		t.Cleanup(func() { close(unblock) })
		_, err := boundedSFTPOp(context.Background(), sc, client, "read", "/tmp/x", func() (string, error) {
			<-unblock
			return "", nil
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
		sc.mu.Lock()
		defer sc.mu.Unlock()
		assert.Nil(t, sc.client, "a wedged connection must be torn down so the next op re-dials")
	})

	t.Run("caller cancellation invalidates the connection", func(t *testing.T) {
		client := newInMemorySFTPClient(t)
		sc := &sftpConn{key: "test-bounded-cancel", client: client, lastUsed: time.Now()}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		unblock := make(chan struct{})
		t.Cleanup(func() { close(unblock) })
		_, err := boundedSFTPOp(ctx, sc, client, "read", "/tmp/x", func() (string, error) {
			<-unblock
			return "", nil
		})
		require.ErrorIs(t, err, context.Canceled)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		assert.Nil(t, sc.client, "an abandoned connection must be torn down to unblock the stuck op")
	})

	t.Run("timeout returns promptly even when client teardown blocks", func(t *testing.T) {
		client := newWedgedSFTPClient(t)
		sc := &sftpConn{key: "test-bounded-blocking-close", client: client, lastUsed: time.Now()}
		unblock := make(chan struct{})
		t.Cleanup(func() { close(unblock) })
		start := time.Now()
		_, err := boundedSFTPOp(context.Background(), sc, client, "read", "/tmp/x", func() (string, error) {
			<-unblock
			return "", nil
		})
		require.Error(t, err)
		assert.Less(t, time.Since(start), 5*time.Second,
			"bounded op must not be delayed by connection teardown")
		sc.mu.Lock()
		defer sc.mu.Unlock()
		assert.Nil(t, sc.client)
	})

	t.Run("timeout on a stale client does not tear down a replacement", func(t *testing.T) {
		staleClient := newInMemorySFTPClient(t)
		replacement := newInMemorySFTPClient(t)
		sc := &sftpConn{key: "test-bounded-replaced", client: replacement, lastUsed: time.Now()}
		unblock := make(chan struct{})
		t.Cleanup(func() { close(unblock) })
		_, err := boundedSFTPOp(context.Background(), sc, staleClient, "read", "/tmp/x", func() (string, error) {
			<-unblock
			return "", nil
		})
		require.Error(t, err)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		assert.Same(t, replacement, sc.client,
			"a timeout from an obsolete client must not tear down the current connection")
	})
}

// TestRemoteAgentInstallCommand runs the install megacommand under a real
// shell, standing in for the remote login shell: the streamed stdin must land
// installed and executable at the final path, its gc mode must be invoked
// (install-scoped stale-binary cleanup), and the checksum of the streamed
// bytes must be read back after the completion marker.
func TestRemoteAgentInstallCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	remotePath := filepath.Join(dir, "side-agent-currentidentity")

	// The streamed "binary" is a script that records how it was invoked,
	// making the chain's gc step (run against the just-installed binary)
	// observable.
	invokedLog := filepath.Join(dir, "invoked")
	content := "#!/bin/sh\necho \"$1\" > " + shellQuote(invokedLog) + "\n"
	cmd := exec.Command("sh", "-c", remoteAgentInstallCommand(remotePath, "fixed"))
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.GreaterOrEqual(t, len(lines), 3, string(out))
	assert.Len(t, strings.Fields(lines[0]), 2, "first output line must be uname -sm")
	assert.Equal(t, remoteInstallCompleteMarker, lines[len(lines)-2])
	checksumFields := strings.Fields(lines[len(lines)-1])
	require.NotEmpty(t, checksumFields)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(content))), checksumFields[0])

	data, err := os.ReadFile(remotePath)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
	info, err := os.Stat(remotePath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0111)
	_, err = os.Stat(remotePath + ".tmp-fixed")
	assert.ErrorIs(t, err, os.ErrNotExist)

	invokedWith, err := os.ReadFile(invokedLog)
	require.NoError(t, err, "install chain must invoke the installed binary's gc mode")
	assert.Equal(t, "gc", strings.TrimSpace(string(invokedWith)))
}

// installFakeSSH puts a fake ssh on PATH whose behavior is given by script
// (a /bin/sh body). The caller's test must not be parallel.
func installFakeSSH(t *testing.T, script string) {
	t.Helper()
	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "ssh"), []byte("#!/bin/sh\n"+script), 0700))
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubAgentBinary points agentBinaryForPlatform at a fixed local file,
// recording the requested platform, so install tests need no real agent
// build. The caller's test must not be parallel.
func stubAgentBinary(t *testing.T, content string) (gotPlatform *string) {
	t.Helper()
	localPath := filepath.Join(t.TempDir(), "agent-binary")
	require.NoError(t, os.WriteFile(localPath, []byte(content), 0700))
	var platform string
	original := agentBinaryForPlatform
	agentBinaryForPlatform = func(targetOS, targetArch string) (string, error) {
		platform = targetOS + "/" + targetArch
		return localPath, nil
	}
	t.Cleanup(func() { agentBinaryForPlatform = original })
	return &platform
}

func TestInstallRemoteAgentVerifiesChecksum(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package hook.
	uploadedPath := filepath.Join(t.TempDir(), "uploaded")
	installFakeSSH(t, fmt.Sprintf(`echo "Linux x86_64"
cat > %s
echo side-agent-install-complete
sha256sum %s 2>/dev/null || shasum -a 256 %s
`, shellQuote(uploadedPath), shellQuote(uploadedPath), shellQuote(uploadedPath)))
	platform := stubAgentBinary(t, "streamed agent bytes")

	err := installRemoteAgent(context.Background(), []string{"remote-host"}, "/tmp/side-agent-testidentity")
	require.NoError(t, err)
	assert.Equal(t, "linux/amd64", *platform)

	uploaded, err := os.ReadFile(uploadedPath)
	require.NoError(t, err)
	assert.Equal(t, "streamed agent bytes", string(uploaded))
}

func TestInstallRemoteAgentChecksumMismatch(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package hook.
	installFakeSSH(t, `echo "Linux x86_64"
cat > /dev/null
echo side-agent-install-complete
echo "deadbeef  /tmp/side-agent-testidentity"
`)
	stubAgentBinary(t, "streamed agent bytes")

	err := installRemoteAgent(context.Background(), []string{"remote-host"}, "/tmp/side-agent-testidentity")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

// TestInstallRemoteAgentSessionExitFailure ensures a session that emits a
// valid marker and matching checksum but exits non-zero is still treated as
// a failed install: the chain has no tolerated trailing steps, so a
// non-zero exit indicates a genuine session failure.
func TestInstallRemoteAgentSessionExitFailure(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package hook.
	uploadedPath := filepath.Join(t.TempDir(), "uploaded")
	installFakeSSH(t, fmt.Sprintf(`echo "Linux x86_64"
cat > %s
echo side-agent-install-complete
sha256sum %s 2>/dev/null || shasum -a 256 %s
exit 3
`, shellQuote(uploadedPath), shellQuote(uploadedPath), shellQuote(uploadedPath)))
	stubAgentBinary(t, "streamed agent bytes")

	err := installRemoteAgent(context.Background(), []string{"remote-host"}, "/tmp/side-agent-testidentity")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after checksum verification")
}

// TestInstallRemoteAgentRequiresCompletionMarker covers the megacommand's ||
// fallthrough: when an install step fails, the checksum fallback may still
// hash a pre-existing final binary correctly, so a matching checksum without
// the completion marker must not be accepted as a successful install.
func TestInstallRemoteAgentRequiresCompletionMarker(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package hook.
	uploadedPath := filepath.Join(t.TempDir(), "uploaded")
	installFakeSSH(t, fmt.Sprintf(`echo "Linux x86_64"
cat > %s
sha256sum %s 2>/dev/null || shasum -a 256 %s
`, shellQuote(uploadedPath), shellQuote(uploadedPath), shellQuote(uploadedPath)))
	stubAgentBinary(t, "streamed agent bytes")

	err := installRemoteAgent(context.Background(), []string{"remote-host"}, "/tmp/side-agent-testidentity")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "completion marker")
}

func TestInstallRemoteAgentTimeout(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package timeout.
	installFakeSSH(t, "exec sleep 60\n")

	originalTimeout := remoteBinaryOpTimeout
	remoteBinaryOpTimeout = 500 * time.Millisecond
	t.Cleanup(func() {
		remoteBinaryOpTimeout = originalTimeout
	})

	started := time.Now()
	err := installRemoteAgent(context.Background(), []string{"remote-host"}, "/tmp/side-agent-testidentity")
	elapsed := time.Since(started)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "install timed out")
	assert.Less(t, elapsed, 2*time.Second)
}

func TestRemoteCommandNotFound(t *testing.T) {
	t.Parallel()

	notFound := exec.Command("sh", "-c", "exit 127")
	_ = notFound.Run()
	assert.True(t, remoteCommandNotFound(notFound))

	otherFailure := exec.Command("sh", "-c", "exit 1")
	_ = otherFailure.Run()
	assert.False(t, remoteCommandNotFound(otherFailure))

	assert.False(t, remoteCommandNotFound(exec.Command("sh")), "unstarted command has no exit status")
}
