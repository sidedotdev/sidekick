package jetstream

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Streamer struct {
	js jetstream.JetStream
}

const (
	EphemeralStreamName  = "SIDEKICK_EMPHEMERAL"
	PersistentStreamName = "SIDEKICK_PERSISTENT"
)

func NewStreamer(nc *nats.Conn) (*Streamer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Ensure the tasks stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:     EphemeralStreamName,
		Subjects: []string{},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create emphemeral stream: %w", err)
	}

	// Ensure the persistent stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name: PersistentStreamName,
		// task changes could have been ephemeral, since it doesn't matter if
		// they're lost, but jetstream.DeliverByStartTimePolicy isn't working
		// with the ephemeral stream for some reason

		// flow action changes and flow events are persistent and should not be
		// lost, they show the history of the flow
		Subjects: []string{"tasks.changes.*", "flow_actions.changes.*.*"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil && err != jetstream.ErrStreamNameAlreadyInUse {
		return nil, fmt.Errorf("failed to create persistent stream: %w", err)
	}

	return &Streamer{js: js}, nil
}
