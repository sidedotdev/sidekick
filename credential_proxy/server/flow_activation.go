package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
)

const flowCapabilityLifetime = time.Hour

// ActivateFlow reuses an authorized route binding and refreshes its lease.
func (c *Capabilities) ActivateFlow(flowID string, requested, trusted map[string]common.CredentialProxyConfig, active []string, allowSideSources bool) (Activation, error) {
	if flowID == "" {
		return Activation{}, errors.New("credential proxy requires a flow ID")
	}
	selected, err := credential_proxy.SelectAuthorized(requested, trusted, active)
	if err != nil {
		return Activation{}, err
	}
	if len(selected) == 0 {
		return Activation{}, nil
	}
	for _, definition := range selected {
		if definition.Auth.Side != nil && !allowSideSources {
			return Activation{}, errors.New("side credential proxy access is disabled by host policy")
		}
	}
	data, err := json.Marshal(struct {
		FlowID      string
		Definitions map[string]common.CredentialProxyConfig
	}{flowID, selected})
	if err != nil {
		return Activation{}, errors.New("invalid flow activation")
	}
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])

	c.activationMu.Lock()
	defer c.activationMu.Unlock()

	c.mu.Lock()
	now := c.currentTime()
	for token, entry := range c.handlers {
		if !now.Before(entry.expiresAt) {
			delete(c.handlers, token)
			continue
		}
		if entry.flowKey == key {
			entry.expiresAt = now.Add(flowCapabilityLifetime)
			c.handlers[token] = entry
			activation := Activation{Token: token, Proxies: copyBindings(entry.bindings), ExpiresAt: entry.expiresAt}
			c.mu.Unlock()
			return activation, nil
		}
	}
	c.mu.Unlock()

	activation, err := c.Activate(selected, trusted, active, allowSideSources)
	if err != nil {
		return Activation{}, err
	}
	c.mu.Lock()
	entry := c.handlers[activation.Token]
	entry.flowKey = key
	entry.bindings = copyBindings(activation.Proxies)
	entry.expiresAt = c.currentTime().Add(flowCapabilityLifetime)
	c.handlers[activation.Token] = entry
	activation.ExpiresAt = entry.expiresAt
	c.mu.Unlock()
	return activation, nil
}

func copyBindings(bindings map[string]Binding) map[string]Binding {
	result := make(map[string]Binding, len(bindings))
	for name, binding := range bindings {
		if binding.Side != nil {
			side := *binding.Side
			binding.Side = &side
		}
		result[name] = binding
	}
	return result
}
