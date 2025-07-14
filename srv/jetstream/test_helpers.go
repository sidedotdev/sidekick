package jetstream

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/nats"
	"sync/atomic"
	"testing"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

var testNatsServerPort atomic.Uint32

func NewTestStreamer(t *testing.T) (*Streamer, error) {
	testNatsServerPort.CompareAndSwap(0, 28666) // base value
	port := int(testNatsServerPort.Add(1))      // ensure unique

	// Create & start test server with unique domain and port
	server, err := nats.NewTestServer(nats.ServerOptions{
		Port:            port,
		JetStreamDomain: "sidekick_test",
		StoreDir:        t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, server.Start(context.Background()))

	nc, err := natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), port))
	require.NoError(t, err)

	streamer, err := NewStreamer(nc)
	require.NoError(t, err)

	return streamer, nil
}
