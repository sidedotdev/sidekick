package srv

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/nats"
	"sidekick/srv/jetstream"
	"sidekick/srv/sqlite"
	"sync/atomic"
	"testing"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

var testNatsDelegatorPort atomic.Uint32

func newTestDelegator(t *testing.T) (*Delegator, jetstream.Streamer, *sqlite.Storage) {
	testNatsDelegatorPort.CompareAndSwap(0, 28900)
	port := int(testNatsDelegatorPort.Add(1))
	server, err := nats.NewTestServer(nats.ServerOptions{
		Port:            port,
		JetStreamDomain: "sidekick_delegator_test",
		StoreDir:        t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, server.Start(context.Background()))

	nc, err := natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), port))
	require.NoError(t, err)

	streamer, err := jetstream.NewStreamer(nc)
	require.NoError(t, err)

	storage := sqlite.NewTestSqliteStorage(t, "delegator_test")

	delegator := &Delegator{
		storage:  storage,
		streamer: streamer,
	}

	return delegator, *streamer, storage
}

func NewTestService(t *testing.T) Service {
	delegator, _, _ := newTestDelegator(t)
	return delegator
}