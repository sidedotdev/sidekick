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
	s := &Streamer{js: nil}
	if nc == nil {
		return s, nil
	}
	err := s.Initialize(nc)
	return s, err
}

func (s *Streamer) Initialize(nc *nats.Conn) error {
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Ensure the tasks stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name: EphemeralStreamName,
		// task changes could have been ephemeral, since it doesn't matter if
		// they're lost. NOTE jetstream.DeliverByStartTimePolicy isn't working
		// with this emphemeral stream, but DeliverNewPolicy is working fine.
		Subjects: []string{"tasks.changes.*"},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to create emphemeral stream: %w", err)
	}

	// Ensure the persistent stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name: PersistentStreamName,
		// flow action changes and flow events are persistent and should not be
		// lost, they show the history of the flow
		Subjects: []string{"flow_actions.changes.*.*", "flow_events.*.*"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil && err != jetstream.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("failed to create persistent stream: %w", err)
	}

	s.js = js
	return nil
}
