package iroh

import (
	"context"
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

func TestEndpointWaitsForRelayTicket(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	handler := relayserver.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
			handler.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	relayURL, err := netaddr.ParseRelayURL(server.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		endpoint *Endpoint
		err      error
	}
	results := make(chan result, 1)
	go func() {
		endpoint, err := NewEndpoint(ctx, irohlib.WithRelayMode(relay.ModeCustomURLs(relayURL)))
		results <- result{endpoint, err}
	}()
	var completed *result
	released := false
	defer func() {
		if !released {
			close(gate)
		}
		cancel()
		if completed == nil {
			r := <-results
			completed = &r
		}
		if completed.endpoint != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			defer cleanupCancel()
			require.NoError(t, completed.endpoint.Close(cleanupCtx))
		}
	}()
	select {
	case <-entered:
	case r := <-results:
		completed = &r
		t.Fatal("endpoint returned before relay handshake")
	case <-ctx.Done():
		t.Fatal("relay handshake was not attempted")
	}
	select {
	case r := <-results:
		completed = &r
		t.Fatal("endpoint returned while relay handshake was blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	released = true
	select {
	case r := <-results:
		completed = &r
	case <-ctx.Done():
		t.Fatal("endpoint did not become ready")
	}
	require.NoError(t, completed.err)
	address, err := endpointticket.Decode(completed.endpoint.Ticket())
	require.NoError(t, err)
	require.Equal(t, []netaddr.RelayURL{relayURL}, address.RelayURLs())
	for _, direct := range address.IPAddrs() {
		require.False(t, direct.Addr().IsUnspecified())
	}
}

func TestEndpointReadinessFailureReleasesSocket(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	relayURL, err := netaddr.ParseRelayURL(server.URL)
	require.NoError(t, err)

	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "canceled"}[canceled], func(t *testing.T) {
			socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
			require.NoError(t, err)
			bind := netip.MustParseAddrPort(socket.LocalAddr().String())
			require.NoError(t, socket.Close())
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			if canceled {
				cancel()
			}
			start := time.Now()
			endpoint, err := NewEndpoint(ctx,
				irohlib.WithBindAddr(bind),
				irohlib.WithRelayMode(relay.ModeCustomURLs(relayURL)),
			)
			require.Nil(t, endpoint)
			if canceled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.Less(t, time.Since(start), 3*time.Second)
			rebound, err := net.ListenPacket("udp4", bind.String())
			require.NoError(t, err)
			require.NoError(t, rebound.Close())
		})
	}
}

func TestReachableAddressPreservesTransportAddresses(t *testing.T) {
	t.Parallel()
	relayURL, err := netaddr.ParseRelayURL("http://localhost:1234")
	require.NoError(t, err)
	address := netaddr.EndpointAddr{}.
		WithRelayURL(relayURL).
		WithIP(netip.MustParseAddrPort("192.0.2.1:1234")).
		WithIP(netip.MustParseAddrPort("[2001:db8::1]:1234")).
		WithAddrs(netaddr.NewCustomAddr(42, []byte("custom endpoint")))
	expected := address.Addrs()
	address = address.
		WithIP(netip.MustParseAddrPort("0.0.0.0:1234")).
		WithIP(netip.MustParseAddrPort("[::]:1234"))

	filtered := reachableAddress(address)
	require.Equal(t, address.ID, filtered.ID)
	require.Equal(t, expected, filtered.Addrs())
}

func TestReachableAddressExpandsWildcardCandidates(t *testing.T) {
	t.Parallel()
	localIPs := []netip.Addr{
		netip.MustParseAddr("192.168.101.100"),
		netip.MustParseAddr("172.16.3.159"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("fd00::1"),
		netip.MustParseAddr("::ffff:192.168.101.100"),
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("::1"),
		netip.MustParseAddr("169.254.1.1"),
		netip.MustParseAddr("fe80::1"),
		netip.MustParseAddr("fe80::1%en0"),
		netip.MustParseAddr("ff02::1"),
		netip.IPv4Unspecified(),
		netip.IPv6Unspecified(),
		{},
	}
	tests := []struct {
		name string
		bind string
		want []string
	}{
		{
			name: "dual stack includes both interfaces",
			bind: "[::]:1234",
			want: []string{"192.168.101.100:1234", "172.16.3.159:1234", "[2001:db8::1]:1234", "[fd00::1]:1234"},
		},
		{
			name: "IPv4 only",
			bind: "0.0.0.0:1234",
			want: []string{"192.168.101.100:1234", "172.16.3.159:1234"},
		},
		{
			name: "mapped IPv4 wildcard",
			bind: "[::ffff:0.0.0.0]:1234",
			want: []string{"192.168.101.100:1234", "172.16.3.159:1234"},
		},
		{
			name: "explicit bind is not expanded",
			bind: "127.0.0.1:1234",
			want: []string{"127.0.0.1:1234"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			relayURL, err := netaddr.ParseRelayURL("https://relay.example.com")
			require.NoError(t, err)
			input := netaddr.EndpointAddr{}.
				WithIP(netip.MustParseAddrPort(tt.bind)).
				WithRelayURL(relayURL).
				WithAddrs(netaddr.NewCustomAddr(42, []byte("custom")))
			got := reachableAddress(input, localIPs...)
			var want []netip.AddrPort
			for _, addr := range tt.want {
				want = append(want, netip.MustParseAddrPort(addr))
			}
			require.ElementsMatch(t, want, got.IPAddrs())
			require.Equal(t, input.ID, got.ID)
			require.Equal(t, input.RelayURLs(), got.RelayURLs())
			require.Contains(t, got.Addrs(), netaddr.NewCustomAddr(42, []byte("custom")))
		})
	}
}
