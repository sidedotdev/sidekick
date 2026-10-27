package sideagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"sidekick/coding/lsp/core"

	"github.com/stretchr/testify/require"
)

type lifecycleLSPClient struct {
	core.LSPClient
	closed atomic.Int32
}

func (c *lifecycleLSPClient) Initialize(context.Context, core.InitializeParams) (core.InitializeResponse, error) {
	return core.InitializeResponse{}, nil
}

func (c *lifecycleLSPClient) Close() error {
	c.closed.Add(1)
	return nil
}

func TestLanguageServersShutdownBeforeFirstRequest(t *testing.T) {
	t.Parallel()
	var servers languageServers
	servers.close()
	require.Nil(t, servers.get(nil), "shutdown must prevent lazy session creation")
}

func TestLanguageServersShutdownClosesClient(t *testing.T) {
	t.Parallel()
	var servers languageServers
	t.Cleanup(servers.close)
	sessions := servers.get(nil)
	require.NotNil(t, sessions)
	client := &lifecycleLSPClient{}
	sessions.LSPClientProvider = func(string) core.LSPClient { return client }
	_, _, release, err := sessions.Acquire(context.Background(), core.ClientScope{BaseDir: t.TempDir()}, "go")
	require.NoError(t, err)
	release()

	servers.close()
	require.EqualValues(t, 1, client.closed.Load())
	require.Nil(t, servers.get(nil))
	servers.close()
	require.EqualValues(t, 1, client.closed.Load(), "repeated shutdown must not close clients twice")
}

func TestLanguageServersShutdownRacesFirstRequest(t *testing.T) {
	t.Parallel()
	for range 100 {
		var servers languageServers
		var sessions *core.Sessions
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			sessions = servers.get(nil)
		}()
		go func() {
			defer workers.Done()
			<-start
			servers.close()
		}()
		close(start)
		workers.Wait()

		if sessions != nil {
			// A cache handed to an admitted request must still be closed by shutdown.
			client := &lifecycleLSPClient{}
			sessions.LSPClientProvider = func(string) core.LSPClient { return client }
			_, _, release, err := sessions.Acquire(context.Background(), core.ClientScope{}, "go")
			if release != nil {
				release()
			}
			require.NoError(t, sessions.Close())
			require.ErrorContains(t, err, "closed")
			require.Zero(t, client.closed.Load(), "shutdown must prevent new client initialization")
		}
		require.Nil(t, servers.get(nil))
	}
}
