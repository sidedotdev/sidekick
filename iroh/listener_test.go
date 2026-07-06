package iroh

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmc/go-iroh/iroh"
)

// TestListenerRoundTrip verifies that data written to a net.Conn accepted from
// the iroh listener round-trips to a stream opened by a separate client
// endpoint dialing over the shared Sidekick ALPN.
func TestListenerRoundTrip(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serverEp, err := iroh.Bind(ctx, iroh.WithALPNs(ALPN))
	require.NoError(t, err)
	defer serverEp.Shutdown(ctx)

	l := newListener(serverEp)
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()

	clientEp, err := iroh.Bind(ctx)
	require.NoError(t, err)
	defer clientEp.Shutdown(ctx)

	conn, err := clientEp.Connect(ctx, serverEp.Addr(), ALPN)
	require.NoError(t, err)
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamConn(ctx)
	require.NoError(t, err)
	defer stream.Close()

	want := []byte("hello over iroh")
	_, err = stream.Write(want)
	require.NoError(t, err)

	got := make([]byte, len(want))
	_, err = io.ReadFull(stream, got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
