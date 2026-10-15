package env

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"golang.org/x/crypto/ssh"
)

type devPodRecoveryTestEnv struct {
	*DevPodEnv
	config   SSHConnConfig
	portFile string
}

func (e *devPodRecoveryTestEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	data, err := os.ReadFile(e.portFile)
	if err != nil {
		return SSHConnConfig{}, err
	}
	config := e.config
	config.Port, err = strconv.Atoi(strings.TrimSpace(string(data)))
	return config, err
}

func TestDevPodNativeSSHRecovery(t *testing.T) {
	for _, tc := range []struct {
		name            string
		dockerAvailable bool
		restored        bool
		operationError  bool
	}{
		{name: "workspace restored", dockerAvailable: true, restored: true},
		{name: "workspace still unreachable", dockerAvailable: true},
		{name: "Docker unavailable"},
		{name: "operation failure is not replayed", dockerAvailable: true, operationError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool { return tc.dockerAvailable },
				func(context.Context) bool { return true },
				func(context.Context) error {
					t.Fatal("connection recovery must not start a running daemon")
					return nil
				},
				func(context.Context) error {
					t.Fatal("connection recovery must not restart Docker")
					return nil
				},
			)
			server := startSSHTestServer(t, sshTestServerOptions{})
			config := server.connConfig()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			deadPort := listener.Addr().(*net.TCPAddr).Port
			require.NoError(t, listener.Close())

			dir := t.TempDir()
			portFile := filepath.Join(dir, "port")
			callsFile := filepath.Join(dir, "calls")
			initialPort, recoveryPort := deadPort, deadPort
			if tc.restored {
				recoveryPort = config.Port
			}
			if tc.operationError {
				initialPort = config.Port
			}
			require.NoError(t, os.WriteFile(portFile, []byte(strconv.Itoa(initialPort)), 0600))
			require.NoError(t, os.WriteFile(callsFile, nil, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "devpod"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$TEST_DEVPOD_RECOVERY_CALLS"
case "$1" in
  ssh) exit 1 ;;
  up) printf '%s' "$TEST_DEVPOD_RECOVERY_PORT" > "$TEST_DEVPOD_RECOVERY_PORT_FILE" ;;
  *) exit 2 ;;
esac
`), 0700))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_DEVPOD_RECOVERY_CALLS", callsFile)
			t.Setenv("TEST_DEVPOD_RECOVERY_PORT_FILE", portFile)
			t.Setenv("TEST_DEVPOD_RECOVERY_PORT", strconv.Itoa(recoveryPort))

			e := &devPodRecoveryTestEnv{
				DevPodEnv: &DevPodEnv{WorkspaceName: "recovery-test"},
				config:    config,
				portFile:  portFile,
			}
			transport := &nativeSSHTransport{key: t.Name(), sshEnv: e}
			t.Cleanup(transport.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			operations := 0
			operationErr := errors.New("remote operation failed")
			err = transport.withClient(ctx, func(_ *nativeSSHConn, client *ssh.Client) error {
				operations++
				if tc.operationError {
					return operationErr
				}
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				return err
			})

			calls, readErr := os.ReadFile(callsFile)
			require.NoError(t, readErr)
			switch {
			case tc.operationError:
				require.ErrorIs(t, err, operationErr)
				assert.Equal(t, 1, operations)
				assert.Empty(t, calls)
			case !tc.dockerAvailable:
				require.Error(t, err)
				converter := temporal.GetDefaultFailureConverter()
				converted := converter.FailureToError(converter.ErrorToFailure(err))
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, converted, &appErr)
				assert.Equal(t, ErrTypeDockerEngineUnavailable, appErr.Type())
				assert.True(t, appErr.NonRetryable())
				details, ok := AsDockerEngineFailure(converted)
				require.True(t, ok)
				assert.NotEmpty(t, details.Host)
				assert.Zero(t, operations)
				assert.Empty(t, calls)
			default:
				assert.Equal(t, 1, strings.Count(string(calls), "up recovery-test --ide none"))
				if tc.restored {
					require.NoError(t, err)
					assert.Equal(t, 1, operations)
				} else {
					require.Error(t, err)
					assert.Zero(t, operations)
				}
			}
		})
	}
}

func TestDevPodSSHRecoveryDoesNotReplaySentCommand(t *testing.T) {
	t.Parallel()
	cause := &agentExecTransportError{cause: errors.New("connection lost after sending")}
	calls := 0
	err := RunWithSSHTransportRecovery(context.Background(), &DevPodEnv{WorkspaceName: "unused"}, func() error {
		calls++
		return cause
	})
	require.ErrorIs(t, err, cause)
	assert.Equal(t, 1, calls)
}
