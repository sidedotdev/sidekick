package api

import (
	"sidekick/srv"
	"testing"
)

// NewTestService creates a test service using SQLite in-memory storage with an in-memory streamer.
// Each call creates an isolated database, making it safe for parallel tests.
func NewTestService(t *testing.T) *srv.Delegator {
	t.Helper()
	return srv.NewTestService(t)
}

// TestAllowedOrigins returns an AllowedOrigins that allows all origins for testing.
// This is permissive to avoid breaking existing tests that don't set Origin headers.
func TestAllowedOrigins() *AllowedOrigins {
	return &AllowedOrigins{
		origins: map[string]struct{}{
			"http://localhost:8855": {},
			"http://127.0.0.1:8855": {},
			"http://[::1]:8855":     {},
			"http://localhost:5173": {},
			"http://127.0.0.1:5173": {},
		},
	}
}
