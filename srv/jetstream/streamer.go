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

func NewStreamer(nc *nats.Conn) (*Streamer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Ensure the tasks stream exists (this is idempotent)
	_, err = js.CreateStream(context.Background(), jetstream.StreamConfig{
		Name:     "TASKS",
		Subjects: []string{"tasks.changes.*"},
		Storage:  jetstream.MemoryStorage, // task changes are ephemeral and don't matter if lost
	})
	if err != nil && err != jetstream.ErrStreamNameAlreadyInUse {
		return nil, fmt.Errorf("failed to create tasks stream, it already exists with a different configuration: %w", err)
	}

	return &Streamer{js: js}, nil
}
