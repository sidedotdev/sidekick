package jetstream

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Streamer struct {
	js jetstream.JetStream
}

const (
	EphemeralStreamName = "SIDEKICK_EPHEMERAL"
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

	// Ensure the ephemeral stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:     EphemeralStreamName,
		Subjects: []string{"tasks.changes.*", "flow_actions.changes.*.*", "flow_events.*.*"},
		Storage:  jetstream.MemoryStorage,
		Discard:  jetstream.DiscardOld,
		MaxBytes: 100 * 1024 * 1024, // 100MB
	})
	if err != nil {
		return fmt.Errorf("failed to create ephemeral stream: %w", err)
	}

	s.js = js
	return nil
}

func (s *Streamer) createConsumer(ctx context.Context, subject, streamMessageStartId string) (jetstream.Consumer, error) {
	var deliveryPolicy jetstream.DeliverPolicy
	var startSeq uint64

	if streamMessageStartId == "" {
		return nil, fmt.Errorf("stream message start id is required when creating a consumer")
	} else if streamMessageStartId == "0" {
		deliveryPolicy = jetstream.DeliverAllPolicy
	} else if streamMessageStartId == "$" {
		deliveryPolicy = jetstream.DeliverNewPolicy
	} else {
		deliveryPolicy = jetstream.DeliverByStartSequencePolicy
		var err error
		startSeq, err = strconv.ParseUint(streamMessageStartId, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid stream message start id: %w", err)
		}
	}

	consumer, err := s.js.OrderedConsumer(ctx, EphemeralStreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects:    []string{subject},
		InactiveThreshold: 5 * time.Minute,
		DeliverPolicy:     deliveryPolicy,
		OptStartSeq:       startSeq,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	return consumer, nil
}
