package lsp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/env"

	"github.com/stretchr/testify/require"
)

type ownedTestClient struct {
	MockLSPClient
	closed atomic.Int32
}

func (c *ownedTestClient) Close() error {
	c.closed.Add(1)
	return nil
}

func definitionRequest(dir string) LSPDefinitionLocationsRequest {
	return LSPDefinitionLocationsRequest{
		EnvContainer: &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}},
		FilePath:     "main.go", Symbols: []string{"Target"},
		PreloadedFileContent: []byte("package fixture\nfunc Target() {}\n"),
	}
}

func TestClientCacheBoundsWorkspaceServers(t *testing.T) {
	t.Parallel()
	var clients []*ownedTestClient
	activities := NewLSPActivities(func(string) LSPClient {
		client := &ownedTestClient{MockLSPClient: MockLSPClient{
			TextDocumentDefinitionFunc: func(context.Context, string, int, int) ([]Location, error) {
				return nil, nil
			},
		}}
		clients = append(clients, client)
		return client
	})
	for _, dir := range []string{"/workspace/a", "/workspace/b", "/workspace/a"} {
		_, err := activities.GetSingleFileDefinitions(context.Background(), definitionRequest(dir))
		require.NoError(t, err)
	}
	require.Len(t, clients, 3, "revisiting an evicted workspace must create a fresh server")
	require.EqualValues(t, 1, clients[0].closed.Load())
	require.EqualValues(t, 1, clients[1].closed.Load())
	require.EqualValues(t, 0, clients[2].closed.Load())
}

func TestClientCacheClosesFailedInitialization(t *testing.T) {
	t.Parallel()
	client := &ownedTestClient{MockLSPClient: MockLSPClient{
		InitializeFunc: func(context.Context, InitializeParams) (InitializeResponse, error) {
			return InitializeResponse{}, errors.New("initialization failed")
		},
	}}
	activities := NewLSPActivities(func(string) LSPClient { return client })
	_, err := activities.GetSingleFileDefinitions(context.Background(), definitionRequest("/workspace"))
	require.ErrorContains(t, err, "initialization failed")
	require.EqualValues(t, 1, client.closed.Load())
	require.Empty(t, activities.InitializedClients)
}

func TestClientCacheDoesNotEvictActiveRequest(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	finished := make(chan error, 1)
	client := &ownedTestClient{MockLSPClient: MockLSPClient{
		TextDocumentDefinitionFunc: func(ctx context.Context, _ string, _, _ int) ([]Location, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			close(entered)
			<-unblock
			return nil, nil
		},
	}}
	activities := NewLSPActivities(func(string) LSPClient { return client })
	go func() {
		_, err := activities.GetSingleFileDefinitions(context.Background(), definitionRequest("/workspace/a"))
		finished <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := activities.GetSingleFileDefinitions(ctx, definitionRequest("/workspace/b"))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, client.closed.Load())
	close(unblock)
	require.NoError(t, <-finished)
}

func TestClientCacheHonorsSeededClients(t *testing.T) {
	t.Parallel()
	client := &ownedTestClient{MockLSPClient: MockLSPClient{
		TextDocumentDefinitionFunc: func(context.Context, string, int, int) ([]Location, error) {
			return nil, nil
		},
	}}
	activities := &LSPActivities{
		InitializedClients: map[string]LSPClient{"/workspace:golang": client},
	}
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	_, err := activities.GetSingleFileDefinitions(context.Background(), definitionRequest("/workspace"))
	require.NoError(t, err)
	require.Zero(t, client.closed.Load())
}

func TestClientCacheExpiresWhileIdle(t *testing.T) {
	t.Parallel()
	client := &ownedTestClient{}
	activities := NewLSPActivities(func(string) LSPClient { return client })
	activities.idleTimeout = 10 * time.Millisecond
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	_, _, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", nil)
	require.NoError(t, err)
	release()
	require.Eventually(t, func() bool { return client.closed.Load() == 1 }, 2*time.Second, time.Millisecond)
}

func TestClientCacheRecyclesDuringContinuousUse(t *testing.T) {
	t.Parallel()
	var clients []*ownedTestClient
	activities := NewLSPActivities(func(string) LSPClient {
		client := &ownedTestClient{}
		clients = append(clients, client)
		return client
	})
	activities.idleTimeout = time.Hour
	activities.maxClientAge = 30 * time.Millisecond
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	_, _, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", nil)
	require.NoError(t, err)
	release()
	first := clients[0]
	require.Eventually(t, func() bool {
		_, _, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", nil)
		require.NoError(t, err)
		release()
		return first.closed.Load() == 1
	}, 2*time.Second, time.Millisecond)
	require.GreaterOrEqual(t, len(clients), 2)
}

type badIdentityEnv struct{ *fakeSSHEnv }

func (*badIdentityEnv) MarshalJSON() ([]byte, error) {
	return nil, errors.New("identity unavailable")
}

func TestClientCacheFailedAcquisitionRetainsIdleCleanup(t *testing.T) {
	t.Parallel()
	client := &ownedTestClient{}
	activities := NewLSPActivities(func(string) LSPClient { return client })
	activities.idleTimeout = 10 * time.Millisecond
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	_, _, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", nil)
	require.NoError(t, err)
	release()
	container := &env.EnvContainer{Env: &badIdentityEnv{&fakeSSHEnv{
		fakeEnv: &fakeEnv{envType: env.EnvTypeDevPod},
	}}}
	_, _, _, err = activities.findOrInitClient(context.Background(), "/workspace", "golang", container)
	require.ErrorContains(t, err, "identity unavailable")
	require.Eventually(t, func() bool { return client.closed.Load() == 1 }, 2*time.Second, time.Millisecond)
}

type routingTestClient struct {
	ownedTestClient
	container *env.EnvContainer
}

func (c *routingTestClient) SetEnvContainer(container *env.EnvContainer) {
	c.container = container
}

func TestDocumentSyncRetainsRemoteEnvironmentAndLease(t *testing.T) {
	t.Parallel()
	remote := env.EnvContainer{Env: &fakeSSHEnv{fakeEnv: &fakeEnv{
		envType: env.EnvTypeDevPod, workDir: "/workspace",
	}}}
	var clients []*routingTestClient
	var events []string
	opened := make(chan struct{}, 1)
	unblock := make(chan struct{})
	activities := NewLSPActivities(func(string) LSPClient {
		client := &routingTestClient{}
		client.TextDocumentDefinitionFunc = func(context.Context, string, int, int) ([]Location, error) {
			return nil, nil
		}
		client.TextDocumentDidOpenFunc = func(_ context.Context, params DidOpenTextDocumentParams) error {
			require.NotNil(t, client.container)
			require.Same(t, remote.Env, client.container.Env)
			require.Equal(t, "package fixture\n", params.TextDocument.Text)
			events = append(events, "open")
			select {
			case opened <- struct{}{}:
			default:
			}
			<-unblock
			return nil
		}
		client.TextDocumentDidChangeFunc = func(context.Context, DidChangeTextDocumentParams) error {
			events = append(events, "change")
			return nil
		}
		client.TextDocumentDidSaveFunc = func(context.Context, DidSaveTextDocumentParams) error {
			events = append(events, "save")
			return nil
		}
		client.TextDocumentDidCloseFunc = func(context.Context, DidCloseTextDocumentParams) error {
			events = append(events, "close")
			return nil
		}
		clients = append(clients, client)
		return client
	})
	activities.idleTimeout = time.Hour
	activities.maxClientAge = time.Hour
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	input := SyncDocumentActivityInput{
		EnvContainer: remote, FilePath: "main.go", Content: "package fixture\n",
	}
	finished := make(chan error, 1)
	go func() {
		_, err := activities.SyncDocumentActivity(context.Background(), input)
		finished <- err
	}()
	<-opened
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := activities.GetSingleFileDefinitions(ctx, definitionRequest("/other"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, clients[0].closed.Load())
	close(unblock)
	require.NoError(t, <-finished)
	require.Equal(t, []string{"open", "change", "save", "close"}, events)

	_, err = activities.GetSingleFileDefinitions(context.Background(), definitionRequest("/other"))
	require.NoError(t, err)
	require.EqualValues(t, 1, clients[0].closed.Load())
	_, err = activities.SyncDocumentActivity(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, clients, 3)
	require.Same(t, remote.Env, clients[2].container.Env)
	require.Equal(t, []string{"open", "change", "save", "close", "open", "change", "save", "close"}, events)
}

func TestDocumentSyncCancellationClosesOverlayAndClient(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cleanupErr error
	client := &ownedTestClient{MockLSPClient: MockLSPClient{
		TextDocumentDidOpenFunc: func(context.Context, DidOpenTextDocumentParams) error {
			return nil
		},
		TextDocumentDidChangeFunc: func(ctx context.Context, _ DidChangeTextDocumentParams) error {
			cancel()
			return ctx.Err()
		},
		TextDocumentDidCloseFunc: func(ctx context.Context, _ DidCloseTextDocumentParams) error {
			cleanupErr = ctx.Err()
			return cleanupErr
		},
	}}
	activities := NewLSPActivities(func(string) LSPClient { return client })
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	_, err := activities.SyncDocumentActivity(ctx, SyncDocumentActivityInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/workspace"}},
		FilePath:     "main.go", Content: "package fixture\n",
	})
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, cleanupErr, "didClose needs an independent cleanup context")
	require.EqualValues(t, 1, client.closed.Load(), "a partial sync must not leave a reusable overlay")
}

type cancelOnDoneContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelOnDoneContext) Done() <-chan struct{} {
	c.cancel()
	return c.Context.Done()
}

func TestClientCacheRechecksCancellationAfterGateAcquisition(t *testing.T) {
	t.Parallel()
	created := 0
	activities := NewLSPActivities(func(string) LSPClient {
		created++
		return &ownedTestClient{}
	})
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		_, _, release, err := activities.findOrInitClient(cancelOnDoneContext{ctx, cancel}, "/workspace", "golang", nil)
		if release != nil {
			release()
		}
		cancel()
		require.ErrorIs(t, err, context.Canceled)
	}
	require.Zero(t, created)
}

func TestNotificationWrappersRetainRemoteRoutingAfterEviction(t *testing.T) {
	t.Parallel()
	remote := &env.EnvContainer{Env: &fakeSSHEnv{fakeEnv: &fakeEnv{
		envType: env.EnvTypeDevPod, workDir: "/workspace",
	}}}
	tests := []struct {
		name string
		send func(context.Context, *LSPActivities) error
	}{
		{"open", func(ctx context.Context, activities *LSPActivities) error {
			return activities.TextDocumentDidOpenActivity(ctx, TextDocumentDidOpenActivityInput{
				RepoDir: "/workspace", FilePath: "main.go", EnvContainer: remote,
			})
		}},
		{"change", func(ctx context.Context, activities *LSPActivities) error {
			return activities.TextDocumentDidChangeActivity(ctx, TextDocumentDidChangeActivityInput{
				RepoDir: "/workspace", FilePath: "main.go", EnvContainer: remote,
			})
		}},
		{"save", func(ctx context.Context, activities *LSPActivities) error {
			return activities.TextDocumentDidSaveActivity(ctx, TextDocumentDidSaveActivityInput{
				RepoDir: "/workspace", FilePath: "main.go", EnvContainer: remote,
			})
		}},
		{"close", func(ctx context.Context, activities *LSPActivities) error {
			return activities.TextDocumentDidCloseActivity(ctx, TextDocumentDidCloseActivityInput{
				RepoDir: "/workspace", FilePath: "main.go", EnvContainer: remote,
			})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var clients []*routingTestClient
			activities := NewLSPActivities(func(string) LSPClient {
				client := &routingTestClient{}
				client.TextDocumentDidOpenFunc = func(context.Context, DidOpenTextDocumentParams) error { return nil }
				client.TextDocumentDidChangeFunc = func(context.Context, DidChangeTextDocumentParams) error { return nil }
				client.TextDocumentDidSaveFunc = func(context.Context, DidSaveTextDocumentParams) error { return nil }
				client.TextDocumentDidCloseFunc = func(context.Context, DidCloseTextDocumentParams) error { return nil }
				clients = append(clients, client)
				return client
			})
			t.Cleanup(func() { require.NoError(t, activities.Close()) })
			ctx := context.Background()
			_, _, release, err := activities.findOrInitClient(ctx, "/workspace", "golang", remote)
			require.NoError(t, err)
			release()
			local := &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/workspace"}}
			_, _, release, err = activities.findOrInitClient(ctx, "/workspace", "golang", local)
			require.NoError(t, err)
			release()
			require.EqualValues(t, 1, clients[0].closed.Load())
			require.NoError(t, tt.send(ctx, activities))
			require.Len(t, clients, 3)
			require.Same(t, remote, clients[2].container)
			require.EqualValues(t, 1, clients[1].closed.Load())
		})
	}
}
