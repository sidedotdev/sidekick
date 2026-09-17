package server

import (
	"errors"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
)

// Activation is the result of granting a capability for a set of proxies.
type Activation struct {
	Token     string             `json:"token"`
	Proxies   map[string]Binding `json:"proxies,omitempty"`
	ExpiresAt time.Time          `json:"expiresAt,omitempty"`
}

// Activate receives trusted definitions from application wiring, independently
// of their persistence mechanism. allowSideSources gates proxies that would
// expose the host's own provider credentials; it is a process-level policy
// decided by the caller.
func (c *Capabilities) Activate(requested, trusted map[string]common.CredentialProxyConfig, active []string, allowSideSources bool) (Activation, error) {
	selected, err := credential_proxy.SelectAuthorized(requested, trusted, active)
	if err != nil {
		return Activation{}, err
	}
	if len(selected) == 0 {
		return Activation{}, nil
	}
	for _, definition := range selected {
		if definition.Auth.Side != nil && !allowSideSources {
			return Activation{}, errors.New("side credential proxy requires development mode")
		}
	}
	routes, bindings, err := BindRoutes(selected)
	if err != nil {
		return Activation{}, err
	}
	token, err := c.Grant(routes)
	if err != nil {
		return Activation{}, err
	}
	c.mu.Lock()
	entry := c.handlers[token]
	entry.definitions = selected
	c.handlers[token] = entry
	c.mu.Unlock()
	return Activation{Token: token, Proxies: bindings}, nil
}

func (c *Capabilities) RenewAuthorized(token string, trusted map[string]common.CredentialProxyConfig, allowSideSources bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.currentTime()
	entry, exists := c.handlers[token]
	if !exists || !now.Before(entry.expiresAt) {
		delete(c.handlers, token)
		return errors.New("capability unavailable")
	}
	if len(entry.definitions) == 0 {
		return errors.New("capability has no activation authorization")
	}
	active := make([]string, 0, len(entry.definitions))
	for name := range entry.definitions {
		active = append(active, name)
	}
	selected, err := credential_proxy.SelectAuthorized(entry.definitions, trusted, active)
	if err != nil {
		delete(c.handlers, token)
		return errors.New("capability authorization withdrawn")
	}
	for _, definition := range selected {
		if definition.Auth.Side != nil && !allowSideSources {
			delete(c.handlers, token)
			return errors.New("capability authorization withdrawn")
		}
	}
	lifetime := capabilityLifetime
	if entry.flowKey != "" {
		lifetime = flowCapabilityLifetime
	}
	entry.expiresAt = now.Add(lifetime)
	c.handlers[token] = entry
	return nil
}
