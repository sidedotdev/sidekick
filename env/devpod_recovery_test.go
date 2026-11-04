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
				assert.Equal(t, 1, strings.Count(string(calls), "up --id recovery-test --ide none"),
					"calls: %s; err: %v", calls, err)
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

func TestDevPodRecoveryWorkspaceSource(t *testing.T) {
	for _, tc := range []struct {
		name            string
		localSource     bool
		workspaceExists bool
		probeSucceeds   bool
		upFails         bool
		wantError       string
	}{
		{name: "missing workspace restored from local checkout", localSource: true},
		{name: "existing workspace with local checkout", localSource: true, workspaceExists: true},
		{name: "existing workspace without local checkout", workspaceExists: true},
		{name: "missing workspace without local checkout", wantError: "workspace does not exist"},
		{name: "provisioning failure preserved", localSource: true, upFails: true, wantError: "provisioning failed"},
		{name: "successful probe needs no provisioning", probeSucceeds: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool { return true },
				func(context.Context) bool { return true },
				func(context.Context) error {
					t.Fatal("must not start a running daemon")
					return nil
				},
				func(context.Context) error {
					t.Fatal("must not restart Docker")
					return nil
				},
			)
			dir := t.TempDir()
			source := filepath.Join(dir, "local checkout")
			require.NoError(t, os.MkdirAll(source, 0700))
			restoredFile := filepath.Join(dir, "restored")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "devpod"), []byte(`#!/bin/sh
case "$1" in
  ssh)
    if [ "$TEST_PROBE_SUCCEEDS" = true ]; then exit 0; fi
    exit 1
    ;;
  up)
    shift
    source=
    id=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --id) id=$2; shift 2 ;;
        --ide) shift 2 ;;
        *) source=$1; shift ;;
      esac
    done
    if [ "$source" = recovery-source-test ]; then
      echo "attempted to clone https://recovery-source-test" >&2
      exit 1
    fi
    if [ "$id" != recovery-source-test ]; then
      echo "wrong workspace identity" >&2
      exit 1
    fi
    if [ -n "$source" ]; then
      if [ "$source" != "$TEST_LOCAL_SOURCE" ] || [ ! -d "$source" ]; then
        echo "wrong local source" >&2
        exit 1
      fi
    elif [ "$TEST_WORKSPACE_EXISTS" != true ]; then
      echo "workspace does not exist" >&2
      exit 1
    fi
    if [ "$TEST_UP_FAILS" = true ]; then
      echo "provisioning failed" >&2
      exit 1
    fi
    printf '%s' "$id" > "$TEST_RESTORED_FILE"
    ;;
  *) exit 2 ;;
esac
`), 0700))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_LOCAL_SOURCE", source)
			t.Setenv("TEST_RESTORED_FILE", restoredFile)
			t.Setenv("TEST_WORKSPACE_EXISTS", strconv.FormatBool(tc.workspaceExists))
			t.Setenv("TEST_PROBE_SUCCEEDS", strconv.FormatBool(tc.probeSucceeds))
			t.Setenv("TEST_UP_FAILS", strconv.FormatBool(tc.upFails))
			e := &DevPodEnv{WorkspaceName: "recovery-source-test"}
			if tc.localSource {
				e.LocalRepoDir = source
			}
			recovery := e.newSSHTransportRecovery()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cause := &sshDialTransportError{cause: errors.New("workspace unreachable")}
			recovered, err := recovery.recover(ctx, cause)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.False(t, recovered)
				assert.NoFileExists(t, restoredFile)
			} else {
				require.NoError(t, err)
				assert.True(t, recovered)
				if tc.probeSucceeds {
					assert.NoFileExists(t, restoredFile)
				} else {
					restored, err := os.ReadFile(restoredFile)
					require.NoError(t, err)
					assert.Equal(t, e.WorkspaceName, string(restored))
				}
			}
			recovered, err = recovery.recover(ctx, cause)
			require.NoError(t, err)
			assert.False(t, recovered, "recovery must remain bounded to one attempt")
		})
	}
}
