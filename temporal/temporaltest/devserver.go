// Package temporaltest starts throwaway Temporal servers for integration
// tests. Tests must never dial the host's Temporal server: inside sandboxes
// only its read-only proxy is reverse-forwarded, so starting or signalling a
// workflow fails there, and elsewhere test runs would pollute a shared server.
// scripts/lint_temporal_test_ports enforces this.
package temporaltest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// StartDevServer starts an in-memory Temporal server owned by the test and
// returns its frontend address. The server stops when the test ends. The
// first call on a machine downloads the Temporal CLI, which the SDK caches.
func StartDevServer(t testing.TB) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	server, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		LogLevel: "warn",
		Stderr:   os.Stderr,
	})
	require.NoError(t, err, "start throwaway Temporal dev server")
	t.Cleanup(func() { _ = server.Stop() })
	return server.FrontendHostPort()
}
