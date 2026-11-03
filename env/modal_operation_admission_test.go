package env

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/repotools"
	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
)

func modalOperationAdmissionFixture(t *testing.T, replacement bool) (*ModalEnv, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	var active atomic.Bool
	var attempts atomic.Int32
	e := &ModalEnv{
		SandboxName: t.Name(),
		runModalCommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			attempt := attempts.Add(1)
			if attempt == 1 {
				return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
			}
			if replacement && attempt == 2 {
				return EnvRunCommandOutput{ExitStatus: 255}, "connect to host: Connection refused", nil
			}
			if input.activity == nil {
				return EnvRunCommandOutput{}, "", errors.New("expected managed admission")
			}
			active.Store(true)
			input.activity.admit()
			<-ctx.Done()
			active.Store(false)
			return EnvRunCommandOutput{}, "", ctx.Err()
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			return EnvRunCommandOutput{}, errors.New("unexpected API fallback")
		},
	}
	return e, &active, &attempts
}

func TestModalAgentOperationAdmission(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, "legacy")
	if _, err := exec.LookPath("rg"); err != nil {
		t.Fatal("ripgrep is required")
	}
	for _, replacement := range []bool{false, true} {
		name := "unsealed"
		if replacement {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			e, active, attempts := modalOperationAdmissionFixture(t, replacement)
			e.WorkingDirectory = t.TempDir()
			source := "package example\nfunc Target() string { return \"needle\" }\n"
			require.NoError(t, os.WriteFile(filepath.Join(e.WorkingDirectory, "example.go"), []byte(source), 0o600))

			conn := getPooledAgentExecConn(e.sftpConnKey())
			installClient := func() {
				conn.mu.Lock()
				conn.client = startTestAgentClient(t)
				conn.mu.Unlock()
			}
			installClient()
			t.Cleanup(func() { conn.Close() })
			var refreshes atomic.Int32
			e.refreshModalEndpoint = func(context.Context, string) (string, int, error) {
				refreshes.Add(1)
				conn.Close()
				conn = getPooledAgentExecConn(e.sftpConnKey())
				installClient()
				return "replacement", 22, nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			callbacks := 0
			callbackErr := errors.New("callback finished")
			err := e.WithAgent(ctx, func(client *sideagent.Client) error {
				callbacks++
				require.True(t, active.Load(), "no payload may start before admission")
				response, err := client.Exec(ctx, sideagent.ExecRequest{
					Dir: e.WorkingDirectory, Argv: []string{"sh", "-c", "printf x >> marker"},
				})
				require.NoError(t, err)
				require.Empty(t, response.Error)
				require.Zero(t, response.ExitStatus)
				search, err := client.BulkSearchRepository(ctx, e.WorkingDirectory, repotools.BulkSearchRepositoryParams{
					Searches: []repotools.SingleSearchParams{{PathGlob: "example.go", SearchTerm: "needle"}},
				})
				require.NoError(t, err)
				require.Contains(t, search, "func Target()")
				symbols, err := client.BulkGetSymbolDefinitions(ctx, e.WorkingDirectory, repotools.DirectorySymDefRequest{
					Requests: []repotools.FileSymDefRequest{{FilePath: "example.go"}},
				})
				require.NoError(t, err)
				require.Contains(t, symbols.SymbolDefinitions, "func Target()")
				return callbackErr
			})
			require.ErrorIs(t, err, callbackErr)
			require.Equal(t, 1, callbacks)
			require.False(t, active.Load(), "admission must be released after the callback")
			wantAttempts := int32(2)
			if replacement {
				wantAttempts = 3
				require.Equal(t, int32(1), refreshes.Load())
			}
			require.Equal(t, wantAttempts, attempts.Load())
			content, err := os.ReadFile(filepath.Join(e.WorkingDirectory, "marker"))
			require.NoError(t, err)
			require.Equal(t, "x", string(content))
		})
	}
}

type admissionTestSFTPTransport struct {
	SSHTransport
	run func(context.Context, SFTPOp) (any, error)
}

func (t admissionTestSFTPTransport) WithSFTP(ctx context.Context, op SFTPOp) (any, error) {
	return t.run(ctx, op)
}

func TestModalFileOperationAdmission(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, "native")
	original := newNativeSSHTransport
	t.Cleanup(func() { newNativeSSHTransport = original })
	for _, operation := range []string{"read", "write", "mkdir", "stat", "readdir", "remove", "createtemp"} {
		t.Run(operation, func(t *testing.T) {
			e, active, attempts := modalOperationAdmissionFixture(t, false)
			e.WorkingDirectory = "/"
			client := newActivityTestClient(t)
			require.NoError(t, doSFTPWrite(client, "/value", []byte("before"), 0o600))
			dispatched := 0
			newNativeSSHTransport = func(string, []common.PortForwardConfig, SSHCapableEnv) SSHTransport {
				return admissionTestSFTPTransport{run: func(_ context.Context, op SFTPOp) (any, error) {
					dispatched++
					require.True(t, active.Load(), "file access must not precede admission")
					return op.Run(client)
				}}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var err error
			switch operation {
			case "read":
				var data []byte
				data, err = e.ReadFile(ctx, "value")
				require.Equal(t, "before", string(data))
			case "write":
				err = e.WriteFile(ctx, "value", []byte("after"), 0o600)
				data, readErr := doSFTPRead(client, "/value")
				require.NoError(t, readErr)
				require.Equal(t, "after", string(data))
			case "mkdir":
				err = e.MkdirAll(ctx, "directory", 0o755)
			case "stat":
				_, err = e.Stat(ctx, "value")
			case "readdir":
				_, err = e.ReadDir(ctx, "/")
			case "remove":
				err = e.Remove(ctx, "value")
			case "createtemp":
				_, err = e.CreateTemp(ctx, "/", "scratch-*")
			}
			require.NoError(t, err)
			require.Equal(t, 1, dispatched)
			require.Equal(t, int32(2), attempts.Load())
			require.False(t, active.Load())
		})
	}
}

func TestModalAgentAdmissionCancellation(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, "legacy")
	e := &ModalEnv{
		SandboxName: t.Name(),
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
		},
	}
	conn := getPooledAgentExecConn(e.sftpConnKey())
	conn.mu.Lock()
	conn.client = startTestAgentClient(t)
	conn.mu.Unlock()
	t.Cleanup(conn.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	calls := 0
	err := e.WithAgent(ctx, func(*sideagent.Client) error {
		calls++
		return nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, calls)
}

func TestModalOperationAdmissionRefusal(t *testing.T) {
	for _, operation := range []string{"agent", "file"} {
		for _, failure := range []string{"persistent", "cancelled", "ordinary", "ambiguous"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				t.Setenv(SSHTransportEnvVar, "native")
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				var attempts atomic.Int32
				wantErr := errors.New("sandbox sealed")
				if failure == "ambiguous" {
					wantErr = errors.New("connection closed after dispatch")
				}
				e := &ModalEnv{
					SandboxName: t.Name(),
					runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
						attempts.Add(1)
						switch failure {
						case "persistent":
							return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
						case "cancelled":
							cancel()
							return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
						default:
							return EnvRunCommandOutput{}, "", wantErr
						}
					},
				}
				dispatched := 0
				original := newNativeSSHTransport
				newNativeSSHTransport = func(string, []common.PortForwardConfig, SSHCapableEnv) SSHTransport {
					return admissionTestSFTPTransport{run: func(context.Context, SFTPOp) (any, error) {
						dispatched++
						return []byte("unexpected"), nil
					}}
				}
				t.Cleanup(func() { newNativeSSHTransport = original })
				var err error
				if operation == "agent" {
					t.Setenv(SSHTransportEnvVar, "legacy")
					conn := getPooledAgentExecConn(e.sftpConnKey())
					conn.mu.Lock()
					conn.client = startTestAgentClient(t)
					conn.mu.Unlock()
					t.Cleanup(conn.Close)
					err = e.WithAgent(ctx, func(*sideagent.Client) error {
						dispatched++
						return nil
					})
				} else {
					_, err = e.ReadFile(ctx, "/value")
				}
				switch failure {
				case "persistent":
					require.ErrorIs(t, err, context.DeadlineExceeded)
				case "cancelled":
					require.ErrorIs(t, err, context.Canceled)
				default:
					require.ErrorIs(t, err, wantErr)
				}
				require.Equal(t, int32(1), attempts.Load())
				require.Zero(t, dispatched)
			})
		}
	}
}

func TestModalAdmittedOperationsAreNotReplayed(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, "legacy")
	e, active, attempts := modalOperationAdmissionFixture(t, false)
	conn := getPooledAgentExecConn(e.sftpConnKey())
	conn.mu.Lock()
	conn.client = startTestAgentClient(t)
	conn.mu.Unlock()
	t.Cleanup(conn.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, opErr := range []error{
		errModalCommandNotAdmitted,
		errors.New("sandbox sealed"),
		errors.New("connection closed after dispatch"),
	} {
		effects := 0
		err := e.WithAgent(ctx, func(*sideagent.Client) error {
			require.True(t, active.Load())
			effects++
			return opErr
		})
		require.Same(t, opErr, err)
		require.Equal(t, 1, effects)
	}
	require.Equal(t, int32(4), attempts.Load())

	t.Setenv(SSHTransportEnvVar, "native")
	original := newNativeSSHTransport
	t.Cleanup(func() { newNativeSSHTransport = original })
	effects := 0
	ambiguous := errors.New("write response lost")
	newNativeSSHTransport = func(string, []common.PortForwardConfig, SSHCapableEnv) SSHTransport {
		return admissionTestSFTPTransport{run: func(context.Context, SFTPOp) (any, error) {
			require.True(t, active.Load())
			effects++
			return nil, ambiguous
		}}
	}
	require.Same(t, ambiguous, e.WriteFile(ctx, "/value", []byte("payload"), 0o600))
	require.Equal(t, 1, effects)
	require.Equal(t, int32(5), attempts.Load())
}
