package iroh

import (
	"context"

	"github.com/tmc/go-iroh/dns"
	irohlib "github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/watch"
)

// EnableDiscovery publishes signed relay hints through iroh's N0 discovery
// service so saved pairings survive home-relay changes. LAN addresses stay private.
func (e *Endpoint) EnableDiscovery() error {
	return e.startDiscovery(func(secret key.SecretKey) (*irohlib.PkarrPublisher, error) {
		return irohlib.N0PkarrPublisher(secret, nil)
	})
}

func (e *Endpoint) startDiscovery(newPublisher func(key.SecretKey) (*irohlib.PkarrPublisher, error)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return irohlib.ErrEndpointClosed
	}
	if e.closeDiscovery != nil {
		return nil
	}
	publisher, err := newPublisher(e.ep.SecretKey())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	relayStatus := e.ep.HomeRelayStatus()
	go func() {
		defer close(done)
		publishRelayChanges(ctx, relayStatus, publisher)
	}()
	e.closeDiscovery = func() error {
		cancel()
		<-done
		return publisher.Close()
	}
	return nil
}

func publishRelayChanges(ctx context.Context, statuses watch.Observer[*irohlib.RelayStatus], publisher irohlib.AddressPublisher) {
	for status := range statuses.Stream(ctx) {
		if status == nil || !status.IsConnected() {
			continue
		}
		publisher.Publish(dns.NewEndpointData(netaddr.RelayAddr{URL: status.URL}))
	}
}
