package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalSFTPMkdirRecoversAfterConcurrentCancellation(t *testing.T) {
	t.Setenv(SSHTransportEnvVar, "native")
	server := startSSHTestServer(t, sshTestServerOptions{})
	var probes atomic.Int32
	e := &sealedRecoveryTestEnv{
		config: server.connConfig(),
		ModalEnv: &ModalEnv{
			runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
				probes.Add(1)
				return EnvRunCommandOutput{}, "", nil
			},
		},
	}
	transport := sshTransportFor(t.Name(), nil, e)
	t.Cleanup(transport.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	_, err := sftpStat(ctx, transport, dir)
	require.NoError(t, err)

	slowCtx, cancelSlow := context.WithCancel(ctx)
	defer cancelSlow()
	started := make(chan *sftp.Client, 1)
	slowResult := make(chan error, 1)
	go func() {
		_, err := transport.WithSFTP(slowCtx, SFTPOp{
			Name: "stalled",
			Path: dir,
			Run: func(client *sftp.Client) (any, error) {
				started <- client
				return nil, client.Wait()
			},
		})
		slowResult <- err
	}()

	var sharedClient *sftp.Client
	select {
	case sharedClient = <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	ignoreDir := filepath.Join(dir, ".side", "tmp")
	var attempts atomic.Int32
	firstResult := make(chan error, 1)
	_, err = transport.WithSFTP(ctx, SFTPOp{
		Name:            "mkdirall",
		Path:            ignoreDir,
		RetryOnNotExist: true,
		Run: func(client *sftp.Client) (any, error) {
			if attempts.Add(1) == 1 {
				if client != sharedClient {
					err := fmt.Errorf("mkdir did not acquire the stalled shared channel")
					firstResult <- err
					return nil, err
				}
				// Both operations must hold the shared channel before it is
				// invalidated, independently of SSH setup latency.
				cancelSlow()
				_ = client.Wait()
				err := doSFTPMkdirAll(client, ignoreDir, 0o755)
				firstResult <- err
				return nil, err
			}
			return nil, doSFTPMkdirAll(client, ignoreDir, 0o755)
		},
	})
	require.NoError(t, err)
	select {
	case err := <-firstResult:
		require.ErrorIs(t, err, sftp.ErrSSHFxConnectionLost)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-slowResult:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assert.EqualValues(t, 2, attempts.Load())
	assert.EqualValues(t, 1, probes.Load())

	ignorePath := filepath.Join(ignoreDir, "core_ignore")
	content := []byte(".git\n.side/tmp\n")
	require.NoError(t, sftpWriteFile(ctx, transport, ignorePath, content, 0o644))
	got, err := os.ReadFile(ignorePath)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}
