package srv

import (
	"sidekick/srv/jetstream"
	"sidekick/srv/sqlite"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestDelegator(t *testing.T) (*Delegator, jetstream.Streamer, *sqlite.Storage) {
	streamer, err := jetstream.NewTestStreamer(t)
	require.NoError(t, err)

	storage := sqlite.NewTestStorage(t, "delegator_test")

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
