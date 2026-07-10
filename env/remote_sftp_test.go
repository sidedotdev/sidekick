package env

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
