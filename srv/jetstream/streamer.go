package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Streamer struct {
	js jetstream.JetStream
}

func (s *Streamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	taskChan := make(chan domain.Task)
	errChan := make(chan error, 1)

	go func() {
		defer close(taskChan)
		defer close(errChan)

		subject := fmt.Sprintf("tasks.changes.%s", workspaceId)

		var deliveryPolicy jetstream.DeliverPolicy
		var startSeq uint64
		if streamMessageStartId == "0" {
			deliveryPolicy = jetstream.DeliverAllPolicy
		} else if streamMessageStartId == "$" || streamMessageStartId == "" {
			deliveryPolicy = jetstream.DeliverNewPolicy
		} else {
			deliveryPolicy = jetstream.DeliverByStartSequencePolicy
			var err error
			startSeq, err = strconv.ParseUint(streamMessageStartId, 10, 64)
			if err != nil {
				errChan <- fmt.Errorf("invalid stream message start id: %w", err)
				return
			}
		}

		consumer, err := s.js.CreateOrUpdateConsumer(ctx, EphemeralStreamName, jetstream.ConsumerConfig{
			FilterSubjects:    []string{subject},
			InactiveThreshold: 5 * time.Minute,
			DeliverPolicy:     deliveryPolicy,
			OptStartSeq:       startSeq,
		})
		if err != nil && err != jetstream.ErrConsumerNameAlreadyInUse {
			errChan <- fmt.Errorf("failed to create consumer: %w", err)
			return
		}

		consContext, err := consumer.Consume(func(msg jetstream.Msg) {
			var task domain.Task
			if err := json.Unmarshal(msg.Data(), &task); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal task: %w", err)
				return
			}
			select {
			case taskChan <- task:
				msg.Ack()
			case <-ctx.Done():
				return
			}
		})
		if err != nil {
			errChan <- fmt.Errorf("failed to create consume context: %w", err)
			return
		}
		defer consContext.Stop()

		<-ctx.Done()
	}()

	return taskChan, errChan
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
		// task changes is ephemeral, since it doesn't matter if they're lost.
		// NOTE jetstream.DeliverByStartTimePolicy isn't working with this
		// emphemeral stream, but DeliverNewPolicy is working fine.
		Subjects: []string{"tasks.changes.*"},
		Storage:  jetstream.MemoryStorage,
		Discard:  jetstream.DiscardOld,
		MaxBytes: 10 * 1024 * 1024, // 10MB
	})
	if err != nil {
		return fmt.Errorf("failed to create emphemeral stream: %w", err)
	}

	// Ensure the persistent stream exists (this is idempotent)
	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name: PersistentStreamName,
		// flow action changes and flow events are persistent and should not be
		// lost, they show the history of the flow. that said, flow actions'
		// final states are stored and that's sufficient, if we adjust the
		// application to use that in combination with streaming. also, the
		// ChatMessageDeltaEvent should be ephemeral, really any event where the
		// parent is a flowAction
		Subjects: []string{"flow_actions.changes.*.*", "flow_events.*.*"},
		Storage:  jetstream.FileStorage,
		Discard:  jetstream.DiscardNew,   // prevent publishing at this point, to avoid losing messages
		MaxBytes: 5 * 1024 * 1024 * 1024, // 5GB
	})
	if err != nil && err != jetstream.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("failed to create persistent stream: %w", err)
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
		deliveryPolicy = jetstream.DeliverLastPolicy
	} else {
		deliveryPolicy = jetstream.DeliverByStartSequencePolicy
		var err error
		startSeq, err = strconv.ParseUint(streamMessageStartId, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid stream message start id: %w", err)
		}
	}

	consumer, err := s.js.OrderedConsumer(ctx, PersistentStreamName, jetstream.OrderedConsumerConfig{
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
