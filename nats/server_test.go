package nats

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"
)

func TestGetOrNewServerConcurrentCallers(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())
	t.Setenv("SIDE_NATS_SERVER_PORT", "-1")
	serve = nil
	serveErr = nil
	serveOnce = sync.Once{}
	t.Cleanup(func() {
		serve = nil
		serveErr = nil
		serveOnce = sync.Once{}
	})

	type result struct {
		server *Server
		err    error
	}
	const callers = 64
	start := make(chan struct{})
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			server, err := GetOrNewServer()
			results <- result{server: server, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	first := <-results
	require.NoError(t, first.err)
	require.NotNil(t, first.server)
	defer first.server.natsServer.Shutdown()
	for result := range results {
		require.NoError(t, result.err)
		require.Same(t, first.server, result.server)
	}
}

func TestGetOrNewServerCachesInitializationError(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", "")
	dataHome := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(dataHome, nil, 0600))
	originalDataHome := xdg.DataHome
	xdg.DataHome = dataHome
	serve = nil
	serveErr = nil
	serveOnce = sync.Once{}
	t.Cleanup(func() {
		xdg.DataHome = originalDataHome
		serve = nil
		serveErr = nil
		serveOnce = sync.Once{}
	})

	server, firstErr := GetOrNewServer()
	require.Nil(t, server)
	require.ErrorContains(t, firstErr, "failed to get Sidekick data home")

	type result struct {
		server *Server
		err    error
	}
	const callers = 16
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server, err := GetOrNewServer()
			results <- result{server: server, err: err}
		}()
	}
	wg.Wait()
	close(results)

	for result := range results {
		require.Nil(t, result.server)
		require.Error(t, result.err)
		require.Same(t, firstErr, result.err)
	}
}
