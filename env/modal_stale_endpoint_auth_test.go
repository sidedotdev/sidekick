package env

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// staleEndpointTestEnv aims a Modal env's SSH config at whatever endpoint the
// env currently records, so a recovery that moves SSHHost/SSHPort shows up as
// a connection to a different server.
type staleEndpointTestEnv struct {
	*ModalEnv
	config SSHConnConfig
}

func (e *staleEndpointTestEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	config := e.config
	config.Host = e.SSHHost
	config.Port = e.SSHPort
	return config, nil
}

func (e *staleEndpointTestEnv) SSHArgs(ctx context.Context) ([]string, error) {
	config, err := e.SSHConnConfig(ctx)
	if err != nil {
		return nil, err
	}
	return config.LegacyArgs(), nil
}

// A sandbox's sshd rejects this host's key both when authorized_keys was
// clobbered and when the recorded tunnel endpoint now belongs to a stranger (a
// restored sandbox keeps its name but not its tunnel). Host keys are unpinned,
// so the handshake cannot tell the two apart; only re-resolving the endpoint
// can. Recovery therefore refreshes first and reinstalls the key only at an
// endpoint confirmed to be the sandbox's own.
func TestModalSFTPRecoversFromRejectedKey(t *testing.T) {
	cases := []struct {
		name string
		// startStale records the stranger's endpoint in the env.
		startStale bool
		// keyClobbered makes the sandbox reject the key until it is repaired.
		keyClobbered bool
		// refreshStays keeps every refresh pointed at the stale endpoint, so
		// recovery can never succeed and must give up within its budget.
		refreshStays  bool
		wantRepairs   int
		wantRefreshes int
		wantErr       bool
	}{
		{name: "stale endpoint is re-resolved without a repair", startStale: true, wantRefreshes: 1},
		{name: "clobbered key is reinstalled at the confirmed endpoint", keyClobbered: true, wantRefreshes: 1, wantRepairs: 1},
		{name: "stale endpoint and clobbered key", startStale: true, keyClobbered: true, wantRefreshes: 2, wantRepairs: 1},
		{name: "refresh keeps returning the stale endpoint", startStale: true, refreshStays: true,
			wantRefreshes: maxModalRecoveryRounds, wantRepairs: 1, wantErr: true},
	}
	for _, kind := range []string{"native", "legacy"} {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				if kind == "legacy" {
					skipWithoutSSHBinary(t)
				}
				t.Setenv(SSHTransportEnvVar, kind)
				var keyInstalled atomic.Bool
				keyInstalled.Store(!tc.keyClobbered)
				// Each server authorizes only its own client key, so the
				// current sandbox's identity is rejected by the stale one.
				stale := startSSHTestServer(t, sshTestServerOptions{})
				var current *sshTestServer
				current = startSSHTestServer(t, sshTestServerOptions{
					KeyAuthorized: keyInstalled.Load,
					SFTPHandler: func(channel ssh.Channel) {
						if err := sideagent.ServeSFTP(sessionReadWriteCloser{channel}); err != nil {
							reportHarnessError(current.errors, err)
						}
					},
				})
				config := current.connConfig()
				config.KnownHostsFiles = append(config.KnownHostsFiles, stale.knownHostsPath)
				staleConfig := stale.connConfig()
				startConfig := config
				if tc.startStale {
					startConfig = staleConfig
				}

				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				var events []string
				operations := 0
				e := &staleEndpointTestEnv{
					config: config,
					ModalEnv: &ModalEnv{
						SandboxName: t.Name(),
						SSHHost:     startConfig.Host,
						SSHPort:     startConfig.Port,
						// Readiness probes must not be what moves the endpoint.
						runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
							return EnvRunCommandOutput{}, "", nil
						},
						runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
							events = append(events, "repair")
							keyInstalled.Store(true)
							return EnvRunCommandOutput{}, nil
						},
						refreshModalEndpoint: func(context.Context, string) (string, int, error) {
							events = append(events, "refresh")
							if tc.refreshStays {
								return staleConfig.Host, staleConfig.Port, nil
							}
							return config.Host, config.Port, nil
						},
					},
				}
				transport := sshTransportFor(t.Name(), nil, e)
				t.Cleanup(transport.Close)

				dir := t.TempDir()
				value, err := transport.WithSFTP(ctx, SFTPOp{
					Name: "stat",
					Path: dir,
					Run: func(client *sftp.Client) (any, error) {
						operations++
						return client.Stat(dir)
					},
				})

				if tc.wantErr {
					require.Error(t, err)
					assert.Zero(t, operations, "an operation must never be dispatched over a connection that was not established")
					assert.Equal(t, staleConfig.Port, e.SSHPort)
				} else {
					require.NoError(t, err)
					require.NotNil(t, value)
					assert.Equal(t, 1, operations)
					assert.Equal(t, config.Port, e.SSHPort)
				}
				assert.Equal(t, tc.wantRepairs, countOf(events, "repair"), "events: %v", events)
				assert.Equal(t, tc.wantRefreshes, countOf(events, "refresh"), "events: %v", events)
				if tc.wantRepairs > 0 {
					assert.Equal(t, "refresh", events[0], "the endpoint must be confirmed before the key is repaired: %v", events)
				}
				requireNoHarnessErrors(t, stale.errors)
				requireNoHarnessErrors(t, current.errors)
			})
		}
	}
}

func countOf(events []string, event string) int {
	n := 0
	for _, e := range events {
		if e == event {
			n++
		}
	}
	return n
}

// A refresh that restored the sandbox from a snapshot rolled its filesystem
// back. File operations have no stderr to carry that, so the recovery must log
// the notice; an ordinary endpoint move must not cry wolf.
func TestModalSSHTransportRecoveryLogsSnapshotRestore(t *testing.T) {
	for _, restored := range []bool{true, false} {
		name := "restored"
		if !restored {
			name = "endpoint moved without restore"
		}
		t.Run(name, func(t *testing.T) {
			refresh := modalEndpointRefresh{
				SSHHost:         "r450.modal.host",
				SSHPort:         40001,
				Restored:        true,
				SnapshotImageId: "im-transport-restore",
			}
			notice := refresh.restoreNotice("side--transport")
			refresh.Restored = restored
			previous := refreshModalEndpointDetailed
			t.Cleanup(func() { refreshModalEndpointDetailed = previous })
			refreshModalEndpointDetailed = func(context.Context, string) (modalEndpointRefresh, error) {
				return refresh, nil
			}

			var logs bytes.Buffer
			ctx := zerolog.New(&logs).WithContext(context.Background())
			e := &ModalEnv{SandboxName: "side--transport", SSHHost: "r436.modal.host", SSHPort: 33175}
			recovered, err := e.newSSHTransportRecovery().recover(ctx,
				errors.New("ssh: connect to host r436.modal.host port 33175: Connection refused"))

			require.NoError(t, err)
			assert.True(t, recovered)
			assert.Equal(t, "r450.modal.host", e.SSHHost)
			assert.Equal(t, 40001, e.SSHPort)
			if restored {
				assert.Contains(t, logs.String(), notice)
			} else {
				assert.NotContains(t, logs.String(), "restored from its last snapshot")
			}
		})
	}
}
