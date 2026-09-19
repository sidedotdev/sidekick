package iroh

import (
	"context"
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
}
