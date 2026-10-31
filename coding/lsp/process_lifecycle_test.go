package lsp

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingPipe struct {
	closed bool
}

func (p *failingPipe) Write(data []byte) (int, error) { return len(data), nil }

func (p *failingPipe) Close() error {
	p.closed = true
	return errors.New("writer close failed")
}

type trackedReader struct {
	io.Reader
	closed bool
}

func (r *trackedReader) Close() error {
	r.closed = true
	return nil
}

func TestReadWriteCloserClosesBothPipesOnError(t *testing.T) {
	t.Parallel()
	reader := &trackedReader{Reader: strings.NewReader("")}
	writer := &failingPipe{}
	transport := &ReadWriteCloser{Reader: reader, WriteCloser: writer}

	require.Error(t, transport.Close())
	require.True(t, writer.closed)
	require.True(t, reader.closed)
}

func TestGoplsIsOwnedRatherThanShared(t *testing.T) {
	t.Parallel()
	require.False(t, slices.ContainsFunc(goplsServerArgs, func(arg string) bool {
		return strings.HasPrefix(arg, "-remote") || strings.HasPrefix(arg, "-debug") ||
			strings.HasPrefix(arg, "-rpc.trace") || strings.HasPrefix(arg, "-logfile")
	}), "desktop clients must not leave a shared daemon or unbounded trace logs")
}

func TestServerTransportCloseReapsOwnedProcess(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		script       string
		requiresKill bool
	}{
		{name: "cooperative", script: "printf ready; while IFS= read -r line; do :; done"},
		{name: "forced", script: "printf ready; exec sleep 30", requiresKill: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("sh", "-c", tt.script)
			transport, err := startServerTransport(cmd)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, transport.Close()) })

			ready := make([]byte, len("ready"))
			_, err = io.ReadFull(transport, ready)
			require.NoError(t, err)
			require.Equal(t, "ready", string(ready))

			require.NoError(t, transport.Close())
			require.NotNil(t, cmd.ProcessState)
			require.ErrorIs(t, cmd.Process.Kill(), os.ErrProcessDone)
			if tt.requiresKill {
				require.False(t, cmd.ProcessState.Exited())
			}
			require.NoError(t, transport.Close())
		})
	}
}

func TestReadWriteCloserAfterPipesAlreadyClosed(t *testing.T) {
	t.Parallel()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, writer.Close())
	transport := &ReadWriteCloser{Reader: reader, WriteCloser: writer}
	require.NoError(t, transport.Close())
	require.NoError(t, transport.Close())
}
