package env

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSFTPInterruptionLogging(t *testing.T) {
	originalTimeout := sftpOpTimeout
	t.Cleanup(func() { sftpOpTimeout = originalTimeout })

	for _, transportKind := range []string{"native", "legacy"} {
		for _, cause := range []string{"cancellation", "caller deadline", "operation timeout"} {
			t.Run(transportKind+"/"+cause, func(t *testing.T) {
				sftpOpTimeout = 10 * time.Second
				path := t.TempDir()
				started := make(chan struct{}, 2)
				op := SFTPOp{
					Name: "read",
					Path: path,
					Run: func(client *sftp.Client) (any, error) {
						started <- struct{}{}
						return nil, client.Wait()
					},
				}

				var run func(context.Context) error
				if transportKind == "native" {
					server := startSSHTestServer(t, sshTestServerOptions{})
					transport := nativeTestTransport(t, server, t.Name())
					warmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, err := sftpStat(warmCtx, transport, path)
					require.NoError(t, err)
					run = func(ctx context.Context) error {
						_, err := transport.WithSFTP(ctx, op)
						return err
					}
				} else {
					client := newInMemorySFTPClient(t)
					conn := &sftpConn{key: t.Name(), client: client, lastUsed: time.Now()}
					run = func(ctx context.Context) error {
						_, err := boundedSFTPOp(ctx, conn, client, op.Name, op.Path, func() (any, error) {
							return op.Run(client)
						})
						return err
					}
				}

				budget := 5 * time.Second
				wantCause := context.Canceled
				if cause == "caller deadline" {
					budget = 100 * time.Millisecond
					wantCause = context.DeadlineExceeded
				} else if cause == "operation timeout" {
					sftpOpTimeout = 50 * time.Millisecond
					wantCause = context.DeadlineExceeded
				}
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				var logs bytes.Buffer
				logger := zerolog.New(zerolog.SyncWriter(&logs))
				ctx = logger.WithContext(ctx)

				result := make(chan error, 1)
				go func() { result <- run(ctx) }()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("SFTP operation did not start:", ctx.Err())
				}
				if cause == "cancellation" {
					cancel()
				}
				select {
				case err := <-result:
					require.Error(t, err)
					if cause != "operation timeout" {
						require.ErrorIs(t, err, wantCause)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("SFTP interruption did not return")
				}

				decoder := json.NewDecoder(&logs)
				found := false
				for {
					var event map[string]any
					err := decoder.Decode(&event)
					if err == io.EOF {
						break
					}
					require.NoError(t, err)
					if event["error"] != wantCause.Error() {
						continue
					}
					found = true
					assert.Equal(t, op.Name, event["operation"])
					assert.Equal(t, path, event["path"])
					assert.NotEmpty(t, event["remote"])
					if cause == "operation timeout" {
						assert.NotContains(t, event, "callerError")
					} else {
						assert.Equal(t, wantCause.Error(), event["callerError"])
					}
				}
				require.True(t, found, "missing diagnostic for shared SFTP interruption")
			})
		}
	}
}
