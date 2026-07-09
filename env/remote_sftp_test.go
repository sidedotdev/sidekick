package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
