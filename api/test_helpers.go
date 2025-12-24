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
