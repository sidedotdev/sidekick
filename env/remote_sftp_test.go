package env

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// startSleepProcess launches a cheap long-running local process (standing in
// for the remote ssh+side-sftp chain) whose reaping can be observed in tests.
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
