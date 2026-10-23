package env

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type modalNativeRecoveryEnv struct {
	*ModalEnv
	config SSHConnConfig
}

func (e *modalNativeRecoveryEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	config := e.config
	config.Host = e.SSHHost
	config.Port = e.SSHPort
	return config, nil
}

func TestModalNativeConnectionRecovery(t *testing.T) {
	for _, recoverEndpoint := range []bool{true, false} {
		name := "refresh stays unreachable"
		if recoverEndpoint {
			name = "refresh reaches live server"
		}
		t.Run(name, func(t *testing.T) {
			server := startSSHTestServer(t, sshTestServerOptions{})
			config := server.connConfig()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			deadPort := listener.Addr().(*net.TCPAddr).Port
			require.NoError(t, listener.Close())

			refreshes := 0
			e := &modalNativeRecoveryEnv{
				ModalEnv: &ModalEnv{
					SandboxName: t.Name(),
					SSHHost:     config.Host,
					SSHPort:     deadPort,
					refreshModalEndpoint: func(context.Context, string) (string, int, error) {
						refreshes++
						if recoverEndpoint {
							return config.Host, config.Port, nil
						}
						return config.Host, deadPort, nil
					},
				},
				config: config,
			}
			transport := &nativeSSHTransport{key: t.Name(), sshEnv: e}
			t.Cleanup(transport.Close)
			operations := 0
			err = transport.withClient(context.Background(), func(_ *nativeSSHConn, client *ssh.Client) error {
				operations++
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				return err
			})
			if recoverEndpoint {
				require.NoError(t, err)
				assert.Equal(t, 1, refreshes)
				assert.Equal(t, 1, operations)
			} else {
				require.Error(t, err)
				assert.Equal(t, maxModalRecoveryRounds, refreshes, "refreshing is bounded like RunCommand's recovery rounds")
				assert.Zero(t, operations)
			}
		})
	}
}

func TestModalNativeRecoveryDoesNotReplayOperationError(t *testing.T) {
	server := startSSHTestServer(t, sshTestServerOptions{})
	config := server.connConfig()
	refreshes := 0
	e := &modalNativeRecoveryEnv{
		ModalEnv: &ModalEnv{
			SandboxName: t.Name(),
			SSHHost:     config.Host,
			SSHPort:     config.Port,
			refreshModalEndpoint: func(context.Context, string) (string, int, error) {
				refreshes++
				return config.Host, config.Port, nil
			},
		},
		config: config,
	}
	transport := &nativeSSHTransport{key: t.Name(), sshEnv: e}
	t.Cleanup(transport.Close)
	operations := 0
	cause := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	err := transport.withClient(context.Background(), func(_ *nativeSSHConn, _ *ssh.Client) error {
		operations++
		return cause
	})
	require.ErrorIs(t, err, cause)
	assert.Equal(t, 1, operations)
	assert.Zero(t, refreshes)
}
