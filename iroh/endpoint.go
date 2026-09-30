// Package iroh provides a self-contained transport that serves Sidekick's REST
// API over an iroh peer-to-peer QUIC endpoint. It intentionally depends on
// nothing from the api or srv packages so it can be reused as a plain
// net.Listener by standard library HTTP servers.
package iroh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"sidekick/common"

	"github.com/rs/zerolog/log"
	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

// ALPN is the Application-Layer Protocol Negotiation value identifying
// Sidekick's REST-over-iroh transport. Peers must present this exact value to
// establish a connection, so it doubles as the application-level auth boundary.
const ALPN = "sidekick/rest/0"

const secretKeyFileName = "iroh_secret.key"

// Endpoint wraps an iroh endpoint bound to the Sidekick ALPN and backed by a
// persistent identity key.
type Endpoint struct {
	ep *iroh.Endpoint
}

// NewEndpoint loads or generates a persistent iroh identity key under the
// Sidekick data home and starts an iroh endpoint bound to the Sidekick ALPN.
// Additional iroh options may be supplied (e.g. to bind a specific address in
// tests); they are applied after the identity and ALPN defaults.
func NewEndpoint(ctx context.Context, opts ...iroh.Option) (*Endpoint, error) {
	sk, err := loadOrCreateSecretKey()
	if err != nil {
		return nil, err
	}
	bindOpts := append([]iroh.Option{
		iroh.WithSecretKey(sk),
		iroh.WithALPNs(ALPN),
		iroh.WithRelayMode(relay.ModeDefault()),
	}, opts...)
	ep, err := iroh.Bind(ctx, bindOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to bind iroh endpoint: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ep.Online(readyCtx); err != nil && !errors.Is(err, iroh.ErrNoRelay) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		ep.Shutdown(cleanupCtx)
		return nil, fmt.Errorf("failed to bring iroh relay online: %w", err)
	}
	return &Endpoint{ep: ep}, nil
}

// Ticket returns the connection ticket string that a remote peer uses to dial
// this endpoint over iroh.
func (e *Endpoint) Ticket() string {
	ips, err := interfaceIPs()
	if err != nil {
		log.Warn().Err(err).Msg("Could not enumerate local iroh address candidates")
	}
	address := e.ep.Addr()
	if local := e.ep.LocalAddr(); local.IsValid() {
		address = address.WithIP(local)
	}
	return endpointticket.Encode(reachableAddress(address, ips...))
}

// NodeID returns the stable ed25519-derived identifier of this endpoint.
func (e *Endpoint) NodeID() string {
	return e.ep.ID().String()
}

// Listener returns a net.Listener that surfaces each accepted iroh
// bidirectional stream as an independent net.Conn, allowing a standard
// http.Server to serve requests over iroh.
func (e *Endpoint) Listener() net.Listener {
	return newListener(e.ep)
}

// Close shuts down the underlying iroh endpoint.
func (e *Endpoint) Close(ctx context.Context) error {
	return e.ep.Shutdown(ctx)
}

func loadOrCreateSecretKey() (key.SecretKey, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return key.SecretKey{}, err
	}
	path := filepath.Join(dataHome, secretKeyFileName)

	seed, err := os.ReadFile(path)
	if err == nil {
		sk, err := key.SecretKeyFromSlice(seed)
		if err != nil {
			return key.SecretKey{}, fmt.Errorf("failed to parse persisted iroh secret key: %w", err)
		}
		return sk, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return key.SecretKey{}, fmt.Errorf("failed to read iroh secret key: %w", err)
	}

	sk, err := key.GenerateSecretKey()
	if err != nil {
		return key.SecretKey{}, fmt.Errorf("failed to generate iroh secret key: %w", err)
	}
	seedArr := sk.Bytes()
	if err := os.WriteFile(path, seedArr[:], 0600); err != nil {
		return key.SecretKey{}, fmt.Errorf("failed to persist iroh secret key: %w", err)
	}
	return sk, nil
}

func reachableAddress(address netaddr.EndpointAddr, localIPs ...netip.Addr) netaddr.EndpointAddr {
	reachable := netaddr.NewEndpointAddr(address.ID)
	for _, transport := range address.Addrs() {
		if direct, ok := transport.(netaddr.IPAddr); ok && direct.Addr.Addr().Unmap().IsUnspecified() {
			for _, ip := range localIPs {
				ip = ip.Unmap()
				if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.Zone() != "" {
					continue
				}
				// The default IPv6 wildcard socket is dual-stack; an explicit
				// IPv4 wildcard socket cannot receive IPv6 traffic.
				if direct.Addr.Addr().Unmap().Is4() && !ip.Is4() {
					continue
				}
				reachable = reachable.WithIP(netip.AddrPortFrom(ip, direct.Addr.Port()))
			}
			continue
		}
		reachable = reachable.WithAddrs(transport)
	}
	return reachable
}

func interfaceIPs() ([]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	var errs []error
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			errs = append(errs, fmt.Errorf("interface %s: %w", iface.Name, err))
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				errs = append(errs, fmt.Errorf("interface %s address: %w", iface.Name, err))
				continue
			}
			ips = append(ips, prefix.Addr())
		}
	}
	return ips, errors.Join(errs...)
}
