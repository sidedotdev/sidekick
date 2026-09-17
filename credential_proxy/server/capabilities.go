package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"sidekick/common"
)

// Capabilities grants access to route subsets without exposing a listener-wide
// credential. Its zero value is ready for use.
type Capabilities struct {
	mu           sync.RWMutex
	activationMu sync.Mutex
	handlers     map[string]capabilityEntry
	transport    http.RoundTripper
	now          func() time.Time
}

func NewCapabilities() *Capabilities {
	return &Capabilities{}
}

func (c *Capabilities) Grant(routes map[string]Route) (string, error) {
	if len(routes) == 0 {
		return "", errors.New("capability requires at least one route")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("failed to generate capability token")
	}
	token := hex.EncodeToString(random)
	handler, err := newHandlerWithTransport(token, routes, c.transport)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handlers == nil {
		c.handlers = make(map[string]capabilityEntry)
	}
	now := c.currentTime()
	for existing, entry := range c.handlers {
		if !now.Before(entry.expiresAt) {
			delete(c.handlers, existing)
		}
	}
	if _, exists := c.handlers[token]; exists {
		return "", errors.New("capability token collision")
	}
	c.handlers[token] = capabilityEntry{
		handler:   handler,
		expiresAt: now.Add(capabilityLifetime),
	}
	return token, nil
}

func (c *Capabilities) Revoke(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.handlers, token)
}

func (c *Capabilities) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	authorization := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || len(token) != 64 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c.mu.Lock()
	entry, exists := c.handlers[token]
	if exists && !c.currentTime().Before(entry.expiresAt) {
		delete(c.handlers, token)
		exists = false
	}
	c.mu.Unlock()
	if !exists {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	entry.handler.ServeHTTP(w, r)
}

const capabilityLifetime = 15 * time.Minute

type capabilityEntry struct {
	handler     http.Handler
	expiresAt   time.Time
	definitions map[string]common.CredentialProxyConfig
	flowKey     string
	bindings    map[string]Binding
}

func (c *Capabilities) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Capabilities) Renew(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.currentTime()
	entry, exists := c.handlers[token]
	if !exists {
		return errors.New("capability unavailable")
	}
	if !now.Before(entry.expiresAt) {
		delete(c.handlers, token)
		return errors.New("capability unavailable")
	}
	if entry.flowKey == "" {
		entry.expiresAt = now.Add(capabilityLifetime)
		c.handlers[token] = entry
	}
	return nil
}
