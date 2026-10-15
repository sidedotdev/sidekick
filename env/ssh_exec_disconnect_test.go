package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeExecDoesNotReplayAfterConnectionLoss(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	transport := nativeTestTransport(t, server, t.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()
	executionsPath := filepath.Join(dir, "executions")
	releasePath := filepath.Join(dir, "release")
	resultCh := make(chan error, 1)
	go func() {
		_, err := transport.Exec(ctx, sideagent.ExecRequest{
			Argv: []string{
				"sh", "-c",
				`printf 'executed\n' >> "$1"; while [ ! -e "$2" ]; do sleep 0.01; done`,
				"test-command", executionsPath, releasePath,
			},
		})
		resultCh <- err
	}()

	require.Eventually(t, func() bool {
		content, err := os.ReadFile(executionsPath)
		return err == nil && string(content) == "executed\n"
	}, 10*time.Second, 10*time.Millisecond, "the command must start before SSH is disconnected")

	server.dropConnections()
	require.NoError(t, os.WriteFile(releasePath, nil, 0600))

	select {
	case err := <-resultCh:
		assert.Error(t, err, "a lost command result must not become a successful replay")
		var transportErr *agentExecTransportError
		assert.ErrorAs(t, err, &transportErr)
	case <-ctx.Done():
		t.Fatal("command did not return after SSH disconnected")
	}

	content, err := os.ReadFile(executionsPath)
	require.NoError(t, err)
	assert.Equal(t, "executed\n", string(content), "the command must execute only once")

	response, err := nativeExecEcho(ctx, transport, "next command")
	require.NoError(t, err)
	assert.Equal(t, "next command\n", string(response.Stdout))
}
