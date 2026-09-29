package env

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
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

func TestModalSealedCommandRecovery(t *testing.T) {
	t.Parallel()
	for _, replacement := range []bool{false, true} {
		name := "same sandbox unseals"
		if replacement {
			name = "replacement uses existing recovery"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			calls, refreshes, apiCalls := 0, 0, 0
			e := &ModalEnv{
				runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
					calls++
					if calls == 1 {
						return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
					}
					if replacement && calls == 2 {
						return EnvRunCommandOutput{ExitStatus: 255}, "connect to host: Connection refused", nil
					}
					return EnvRunCommandOutput{Stdout: "executed"}, "", nil
				},
				refreshModalEndpoint: func(context.Context, string) (string, int, error) {
					refreshes++
					return "replacement", 22, nil
				},
				runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
					apiCalls++
					return EnvRunCommandOutput{}, errors.New("unexpected API fallback")
				},
			}
			output, err := e.RunCommand(ctx, EnvRunCommandInput{Command: "write"})
			require.NoError(t, err)
			assert.Equal(t, "executed", output.Stdout)
			assert.Zero(t, apiCalls)
			if replacement {
				assert.Equal(t, 3, calls)
				assert.Equal(t, 1, refreshes)
			} else {
				assert.Equal(t, 2, calls)
				assert.Zero(t, refreshes)
			}
		})
	}
}

func TestModalSealedCommandCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	e := &ModalEnv{
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			calls++
			cancel()
			return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
		},
	}
	_, err := e.RunCommand(ctx, EnvRunCommandInput{Command: "write"})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

func TestModalAmbiguousAPIRefusalIsNotRetried(t *testing.T) {
	t.Parallel()
	apiCalls := 0
	e := &ModalEnv{
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			return EnvRunCommandOutput{ExitStatus: 255}, "connect to host: Connection refused", nil
		},
		refreshModalEndpoint: func(context.Context, string) (string, int, error) {
			return "sandbox", 22, nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			apiCalls++
			return EnvRunCommandOutput{}, errModalSandboxSealed
		},
	}
	_, err := e.RunCommand(context.Background(), EnvRunCommandInput{Command: "write"})
	require.ErrorIs(t, err, errModalSandboxSealed)
	assert.Equal(t, 1, apiCalls)
}

type sealedRecoveryTestEnv struct {
	*ModalEnv
	config SSHConnConfig
}

func (e *sealedRecoveryTestEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return e.config, nil
}

func TestModalSFTPRecoveryWait(t *testing.T) {
	for _, scenario := range []string{"ready", "replacement", "canceled", "permission"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv(SSHTransportEnvVar, "native")
			server := startSSHTestServer(t, sshTestServerOptions{})
			replacement := startSSHTestServer(t, sshTestServerOptions{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			probes, attempts := 0, 0
			ready := false
			var e *sealedRecoveryTestEnv
			e = &sealedRecoveryTestEnv{
				config: server.connConfig(),
				ModalEnv: &ModalEnv{
					runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
						probes++
						if probes == 1 {
							if scenario == "canceled" {
								cancel()
							}
							return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
						}
						if scenario == "replacement" {
							e.config = replacement.connConfig()
						}
						ready = true
						return EnvRunCommandOutput{}, "", nil
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
					attempts++
					if scenario == "permission" {
						return nil, os.ErrPermission
					}
					if !ready {
						return nil, io.EOF
					}
					return client.Stat(dir)
				},
			})
			switch scenario {
			case "ready", "replacement":
				require.NoError(t, err)
				require.NotNil(t, value)
				assert.Equal(t, 2, attempts)
				assert.Equal(t, 2, probes)
				if scenario == "replacement" {
					assert.Equal(t, 1, server.stats().Sessions)
					assert.Equal(t, 1, replacement.stats().Sessions,
						"the retry must reacquire the endpoint selected by readiness recovery")
				}
			case "canceled":
				require.ErrorIs(t, err, context.Canceled)
				assert.Equal(t, 1, attempts)
			case "permission":
				require.ErrorIs(t, err, os.ErrPermission)
				assert.Equal(t, 1, attempts)
				assert.Zero(t, probes)
			}
		})
	}
}

func (e *sealedRecoveryTestEnv) SSHArgs(context.Context) ([]string, error) {
	return e.config.LegacyArgs(), nil
}

func TestModalSFTPRecoveryAfterRejectedHandshake(t *testing.T) {
	for _, kind := range []string{"native", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(SSHTransportEnvVar, kind)
			var ready atomic.Bool
			var handshakes atomic.Int32
			server := startSSHTestServer(t, sshTestServerOptions{
				SFTPHandler: func(channel ssh.Channel) {
					handshakes.Add(1)
					if !ready.Load() {
						return
					}
					_ = sideagent.ServeSFTP(sessionReadWriteCloser{channel})
				},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			probes, operations := 0, 0
			e := &sealedRecoveryTestEnv{
				config: server.connConfig(),
				ModalEnv: &ModalEnv{
					runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
						probes++
						if probes == 1 {
							return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
						}
						ready.Store(true)
						return EnvRunCommandOutput{}, "", nil
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
			require.NoError(t, err)
			require.NotNil(t, value)
			assert.Equal(t, 2, probes)
			assert.Equal(t, int32(2), handshakes.Load())
			assert.Equal(t, 1, operations, "a refused handshake must not dispatch the operation")
		})
	}
}

func TestModalAdmissionWaitBudget(t *testing.T) {
	t.Parallel()
	t.Run("persistent refusal exhausts budget", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := retryModalAdmission(context.Background(), 10*time.Millisecond, func() error {
			calls++
			return errModalCommandNotAdmitted
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, 1, calls)
	})
	t.Run("budget does not interrupt an admitted command", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := retryModalAdmission(ctx, 10*time.Millisecond, func() error {
			select {
			case <-time.After(30 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		require.NoError(t, err)
	})
}

func TestModalSFTPRecoverySurfacesProbeRestore(t *testing.T) {
	for _, kind := range []string{"native", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			for _, restored := range []bool{true, false} {
				name := "restored"
				if !restored {
					name = "ordinary stderr is not a restore"
				}
				t.Run(name, func(t *testing.T) {
					t.Setenv(SSHTransportEnvVar, kind)
					server := startSSHTestServer(t, sshTestServerOptions{})
					refresh := modalEndpointRefresh{
						SSHHost:         "replacement",
						SSHPort:         22,
						Restored:        true,
						SnapshotImageId: "im-readiness-checkpoint",
					}
					notice := refresh.restoreNotice("side--probe")
					refresh.Restored = restored
					previous := refreshModalEndpointDetailed
					t.Cleanup(func() { refreshModalEndpointDetailed = previous })
					refreshes := 0
					refreshModalEndpointDetailed = func(context.Context, string) (modalEndpointRefresh, error) {
						refreshes++
						return refresh, nil
					}
					probes, operations := 0, 0
					e := &sealedRecoveryTestEnv{
						config: server.connConfig(),
						ModalEnv: &ModalEnv{
							SandboxName: "side--probe",
							runModalCommand: func(_ context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
								require.Equal(t, "true", input.Command)
								probes++
								if probes == 1 {
									return EnvRunCommandOutput{ExitStatus: 255}, "connect to host: Connection refused", nil
								}
								return EnvRunCommandOutput{Stderr: notice}, "", nil
							},
						},
					}
					transport := sshTransportFor(t.Name(), nil, e)
					t.Cleanup(transport.Close)
					var logs bytes.Buffer
					logger := zerolog.New(&logs)
					ctx, cancel := context.WithTimeout(logger.WithContext(context.Background()), 10*time.Second)
					defer cancel()
					dir := t.TempDir()
					value, err := transport.WithSFTP(ctx, SFTPOp{
						Name: "stat",
						Path: dir,
						Run: func(client *sftp.Client) (any, error) {
							operations++
							if operations == 1 {
								return nil, io.EOF
							}
							return client.Stat(dir)
						},
					})
					assert.Equal(t, 2, probes)
					assert.Equal(t, 1, refreshes)
					require.NoError(t, err)
					assert.NotNil(t, value)
					assert.Equal(t, 2, operations)
					if restored {
						assert.Contains(t, logs.String(), notice)
					} else {
						assert.NotContains(t, logs.String(), notice)
					}
				})
			}
		})
	}
}
