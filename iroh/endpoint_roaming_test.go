package iroh

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmc/go-iroh/endpointticket"
	irohlib "github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
	"github.com/tmc/go-iroh/relayserver"
)

func TestSavedTicketReconnectsAfterServerAddressChanges(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	relayServer := httptest.NewServer(relayserver.New())
	defer relayServer.Close()
	relayURL, err := netaddr.ParseRelayURL(relayServer.URL)
	require.NoError(t, err)

	want := bytes.Repeat([]byte("roaming payload\n"), 128*1024)
	startServer := func() *Endpoint {
		t.Helper()
		endpoint, err := NewEndpoint(ctx,
			irohlib.WithBindAddr(netip.MustParseAddrPort("127.0.0.1:0")),
			irohlib.WithRelayMode(relay.ModeCustomURLs(relayURL)),
		)
		require.NoError(t, err)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(want)
			}),
			ReadHeaderTimeout: time.Second,
		}
		listener := endpoint.Listener()
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() {
			_ = server.Close()
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			defer cleanupCancel()
			_ = endpoint.Close(cleanupCtx)
		})
		return endpoint
	}
	verifyTransfer := func(address netaddr.EndpointAddr) {
		t.Helper()
		client, err := irohlib.Bind(ctx,
			irohlib.WithBindAddr(netip.MustParseAddrPort("127.0.0.1:0")),
			irohlib.WithRelayMode(relay.ModeCustomURLs(relayURL)),
		)
		require.NoError(t, err)
		defer client.Shutdown(ctx)
		conn, err := client.Connect(ctx, address, ALPN)
		require.NoError(t, err)
		defer conn.Close()
		stream, err := conn.OpenStreamConn(ctx)
		require.NoError(t, err)
		defer stream.Close()
		require.NoError(t, stream.SetDeadline(time.Now().Add(5*time.Second)))
		_, err = io.WriteString(stream, "GET / HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
		require.NoError(t, err)
		response, err := http.ReadResponse(bufio.NewReader(stream), nil)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		got, err := io.ReadAll(io.LimitReader(response.Body, int64(len(want))+1))
		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	first := startServer()
	saved, err := endpointticket.Decode(first.Ticket())
	require.NoError(t, err)
	require.Equal(t, []netaddr.RelayURL{relayURL}, saved.RelayURLs())
	verifyTransfer(saved)

	oldAddress := first.ep.LocalAddr()
	require.NoError(t, first.Close(ctx))
	// Occupy the old port so a restart cannot accidentally reuse the ticket's
	// direct address and bypass relay-based rediscovery.
	oldSocket, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(oldAddress))
	require.NoError(t, err)
	defer oldSocket.Close()

	restarted := startServer()
	require.Equal(t, first.NodeID(), restarted.NodeID())
	require.NotEqual(t, oldAddress, restarted.ep.LocalAddr())
	verifyTransfer(saved)
}
