package iroh

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmc/go-iroh/endpointticket"
	irohlib "github.com/tmc/go-iroh/iroh"
)

func TestWildcardEndpointTicketExcludesUnspecifiedAddresses(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint, err := NewEndpoint(ctx, irohlib.WithoutRelayTransports())
	require.NoError(t, err)
	defer endpoint.Close(context.Background())

	address, err := endpointticket.Decode(endpoint.Ticket())
	require.NoError(t, err)
	for _, direct := range address.IPAddrs() {
		require.False(t, direct.Addr().IsUnspecified(), "wildcard socket addresses cannot be dialed remotely")
	}
	require.Empty(t, address.RelayURLs())

	port := endpoint.ep.LocalAddr().Port()
	require.NotZero(t, port)
	interfaces, err := net.Interfaces()
	require.NoError(t, err)
	var expected []netip.AddrPort
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		require.NoError(t, err)
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			require.NoError(t, err)
			ip := prefix.Addr().Unmap()
			if ip.IsGlobalUnicast() && !ip.IsLoopback() {
				expected = append(expected, netip.AddrPortFrom(ip, port))
			}
		}
	}
	if len(expected) == 0 {
		t.Skip("no usable non-loopback interface addresses")
	}
	matched := false
	for _, direct := range address.IPAddrs() {
		for _, candidate := range expected {
			matched = matched || direct == candidate
		}
	}
	require.True(t, matched, "ticket must include a usable local interface address")
}
