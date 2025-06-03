package jetstream

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/nats"
	"testing"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

const TestNatsServerPort = 28866

func NewTestStreamer(t *testing.T) (*Streamer, error) {
	// Create & start test server with unique domain and port
	server, err := nats.NewTestServer(nats.ServerOptions{
		Port:            TestNatsServerPort,
		JetStreamDomain: "sidekick_test",
		StoreDir:        t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, server.Start(context.Background()))

	nc, err := natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), TestNatsServerPort))
	require.NoError(t, err)

	streamer, err := NewStreamer(nc)
	require.NoError(t, err)

	return streamer, nil
}
