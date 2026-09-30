package iroh

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	irohlib "github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
	"github.com/tmc/go-iroh/relayserver"
	"github.com/tmc/go-iroh/watch"
)

func TestDiscoveryPublishesRelayChanges(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var packet []byte
	pkarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
			if err != nil {
				http.Error(w, "read failed", http.StatusBadRequest)
				return
			}
			mu.Lock()
			packet = body
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			mu.Lock()
			defer mu.Unlock()
			if packet == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(packet)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer pkarr.Close()
	secret, err := key.GenerateSecretKey()
	require.NoError(t, err)
	publisher, err := irohlib.NewPkarrPublisher(secret, pkarr.URL, nil)
	require.NoError(t, err)
	defer publisher.Close()
	resolver, err := irohlib.NewPkarrResolver(pkarr.URL, nil)
	require.NoError(t, err)

	relayServer := httptest.NewServer(relayserver.New())
	defer relayServer.Close()
	firstRelay, err := netaddr.ParseRelayURL(relayServer.URL)
	require.NoError(t, err)
	endpoint, err := irohlib.Bind(ctx,
		irohlib.WithBindAddr(netip.MustParseAddrPort("127.0.0.1:0")),
		irohlib.WithRelayMode(relay.ModeCustomURLs(firstRelay)),
	)
	require.NoError(t, err)
	defer endpoint.Shutdown(context.Background())
	require.NoError(t, endpoint.Online(ctx))

	initial := *endpoint.HomeRelayStatus().Current()
	statuses := watch.NewValue(&initial)
	publishCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		publishRelayChanges(publishCtx, statuses.Watch(), publisher)
	}()
	defer func() {
		stop()
		<-done
	}()

	checkPublished := func(want netaddr.RelayURL) {
		t.Helper()
		require.Eventually(t, func() bool {
			for item, err := range resolver.Resolve(ctx, secret.Public().EndpointID()) {
				if err != nil {
					return false
				}
				address := item.Addr()
				urls := address.RelayURLs()
				return address.ID == secret.Public().EndpointID() &&
					len(urls) == 1 && urls[0].Equal(want) && len(address.IPAddrs()) == 0
			}
			return false
		}, 3*time.Second, 20*time.Millisecond)
	}
	checkPublished(firstRelay)
	secondRelay, err := netaddr.ParseRelayURL("https://new-relay.example/")
	require.NoError(t, err)
	updated := initial
	updated.URL = secondRelay
	statuses.Set(&updated)
	checkPublished(secondRelay)

	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("discovery watcher did not stop")
	}
}

func TestDiscoveryLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	native, err := irohlib.Bind(ctx, irohlib.WithoutRelayTransports())
	require.NoError(t, err)
	endpoint := &Endpoint{ep: native}
	defer endpoint.Close(context.Background())
	pkarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay-less endpoint should not publish")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer pkarr.Close()
	calls := 0
	factory := func(secret key.SecretKey) (*irohlib.PkarrPublisher, error) {
		calls++
		return irohlib.NewPkarrPublisher(secret, pkarr.URL, nil)
	}
	require.NoError(t, endpoint.startDiscovery(factory))
	require.NoError(t, endpoint.startDiscovery(factory))
	require.Equal(t, 1, calls)
	require.NoError(t, endpoint.Close(ctx))
	require.NoError(t, endpoint.Close(ctx))
	require.ErrorIs(t, endpoint.startDiscovery(factory), irohlib.ErrEndpointClosed)
}
