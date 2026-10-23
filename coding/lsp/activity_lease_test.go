package lsp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/env"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

type activityTestClient struct {
	ownedTestClient
	lose func(error)
}

func (c *activityTestClient) BeginActivity(_ context.Context, onLost func(error)) (func(), error) {
	c.lose = onLost
	return func() {}, nil
}

func TestClientActivityLossCancelsAndInvalidates(t *testing.T) {
	t.Parallel()
	var clients []*activityTestClient
	activities := NewLSPActivities(func(string) LSPClient {
		client := &activityTestClient{lose: func(error) {}}
		client.TextDocumentReferencesFunc = func(ctx context.Context, _ string, _, _ int) ([]Location, error) {
			if len(clients) == 1 {
				client.lose(errors.New("agent disconnected"))
				if ctx.Err() == nil {
					return nil, errors.New("request survived activity loss")
				}
				require.ErrorContains(t, context.Cause(ctx), "agent disconnected")
				require.Positive(t, client.closed.Load())
				return nil, ctx.Err()
			}
			return []Location{{URI: "file:///workspace/main.go"}}, nil
		}
		clients = append(clients, client)
		return client
	})
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	input := FindReferencesActivityInput{
		EnvContainer:         env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/workspace"}},
		RelativeFilePath:     "main.go",
		SymbolText:           "Target",
		PreloadedFileContent: []byte("package fixture\nfunc Target() {}\n"),
	}
	_, err := activities.FindReferencesActivity(context.Background(), input)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, activities.InitializedClients)
	locations, err := activities.FindReferencesActivity(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, locations, 1)
	require.Len(t, clients, 2)
	require.Zero(t, clients[1].closed.Load())
}

type reconnectingActivityClient struct {
	activityTestClient
	connected atomic.Bool
	onBegin   func()
}

func (c *reconnectingActivityClient) IsConnected() bool {
	return c.connected.Load()
}

func (c *reconnectingActivityClient) BeginActivity(ctx context.Context, onLost func(error)) (func(), error) {
	if c.onBegin != nil {
		c.onBegin()
	}
	return c.activityTestClient.BeginActivity(ctx, onLost)
}

func TestClientActivityReconnectsAfterIdleShutdown(t *testing.T) {
	t.Parallel()
	for _, duringWake := range []bool{false, true} {
		name := "already disconnected"
		if duringWake {
			name = "disconnect discovered during wake"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var clients []*reconnectingActivityClient
			activities := NewLSPActivities(func(string) LSPClient {
				client := &reconnectingActivityClient{}
				client.connected.Store(true)
				client.TextDocumentReferencesFunc = func(context.Context, string, int, int) ([]Location, error) {
					if !client.IsConnected() {
						return nil, errors.New("dead cached transport")
					}
					return []Location{{URI: "file:///workspace/main.go"}}, nil
				}
				clients = append(clients, client)
				return client
			})
			t.Cleanup(func() { require.NoError(t, activities.Close()) })
			input := FindReferencesActivityInput{
				EnvContainer:         env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/workspace"}},
				RelativeFilePath:     "main.go",
				SymbolText:           "Target",
				PreloadedFileContent: []byte("package fixture\nfunc Target() {}\n"),
			}
			_, err := activities.FindReferencesActivity(context.Background(), input)
			require.NoError(t, err)
			if duringWake {
				clients[0].onBegin = func() { clients[0].connected.Store(false) }
			} else {
				clients[0].connected.Store(false)
			}
			locations, err := activities.FindReferencesActivity(context.Background(), input)
			require.NoError(t, err, "the first post-idle query must not need a caller retry")
			require.Len(t, locations, 1)
			require.Len(t, clients, 2)
			require.Positive(t, clients[0].closed.Load())
			require.Zero(t, clients[1].closed.Load())
		})
	}
}

func TestClientCacheReplacesDisconnectedJSONRPCConnection(t *testing.T) {
	t.Parallel()
	stream, peer := net.Pipe()
	conn := jsonrpc2.NewConn(context.Background(),
		jsonrpc2.NewBufferedStream(stream, jsonrpc2.VSCodeObjectCodec{}), noopHandler{})
	stale := &Jsonrpc2LSPClient{Conn: conn}
	t.Cleanup(func() { require.NoError(t, stale.Close()) })
	require.True(t, stale.IsConnected())
	require.NoError(t, peer.Close())
	select {
	case <-conn.DisconnectNotify():
	case <-time.After(time.Second):
		t.Fatal("JSON-RPC connection did not observe idle shutdown")
	}
	require.False(t, stale.IsConnected())

	created := 0
	activities := NewLSPActivities(func(string) LSPClient {
		created++
		return &ownedTestClient{MockLSPClient: MockLSPClient{
			TextDocumentReferencesFunc: func(context.Context, string, int, int) ([]Location, error) {
				return []Location{{URI: "file:///workspace/main.go"}}, nil
			},
		}}
	})
	activities.InitializedClients = map[string]LSPClient{"/workspace:golang": stale}
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	locations, err := activities.FindReferencesActivity(context.Background(), FindReferencesActivityInput{
		EnvContainer:         env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/workspace"}},
		RelativeFilePath:     "main.go",
		SymbolText:           "Target",
		PreloadedFileContent: []byte("package fixture\nfunc Target() {}\n"),
	})
	require.NoError(t, err, "the first query must replace the closed JSON-RPC connection")
	require.Len(t, locations, 1)
	require.Equal(t, 1, created)
}
