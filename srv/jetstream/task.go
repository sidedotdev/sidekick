package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Streamer is a JetStream-based task streamer
var _ domain.TaskStreamer = (*Streamer)(nil)

func (s *Streamer) AddTaskChange(ctx context.Context, task domain.Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	subject := fmt.Sprintf("tasks.changes.%s", task.WorkspaceId)
	_, err = s.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish task change: %w", err)
	}

	return nil
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
