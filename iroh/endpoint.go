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
	"os"
	"path/filepath"

	"sidekick/common"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
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
func NewEndpoint(ctx context.Context) (*Endpoint, error) {
	sk, err := loadOrCreateSecretKey()
	if err != nil {
		return nil, err
	}
	ep, err := iroh.Bind(ctx, iroh.WithSecretKey(sk), iroh.WithALPNs(ALPN))
	if err != nil {
		return nil, fmt.Errorf("failed to bind iroh endpoint: %w", err)
	}
	return &Endpoint{ep: ep}, nil
}

// Ticket returns the connection ticket string that a remote peer uses to dial
// this endpoint over iroh.
func (e *Endpoint) Ticket() string {
	return endpointticket.Encode(e.ep.Addr())
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
