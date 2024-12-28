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

func (s *Streamer) GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.Task, string, error) {
	subject := fmt.Sprintf("tasks.changes.%s", workspaceId)

	var deliveryPolicy jetstream.DeliverPolicy
	var startTime *time.Time
	var startSeq uint64
	if streamMessageStartId == "" || streamMessageStartId == "0" {
		deliveryPolicy = jetstream.DeliverAllPolicy
	} else if streamMessageStartId == "$" {
		deliveryPolicy = jetstream.DeliverByStartTimePolicy
		now := time.Now()
		startTime = &now
	} else {
		deliveryPolicy = jetstream.DeliverByStartSequencePolicy
		var err error
		startSeq, err = strconv.ParseUint(streamMessageStartId, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid stream message start id: %w", err)
		}
		startSeq += 1
	}

	consumer, err := s.js.OrderedConsumer(ctx, "TASKS", jetstream.OrderedConsumerConfig{
		FilterSubjects:    []string{subject},
		InactiveThreshold: 5 * time.Minute,
		DeliverPolicy:     deliveryPolicy,
		OptStartSeq:       startSeq,
		OptStartTime:      startTime,
	})
	if err != nil && err != jetstream.ErrConsumerNameAlreadyInUse {
		return nil, "", fmt.Errorf("failed to create consumer: %w", err)
	}

	// Pull messages
	var msgs jetstream.MessageBatch
	if blockDuration == 0 {
		msgs, err = consumer.FetchNoWait(int(maxCount))
	} else {
		msgs, err = consumer.Fetch(int(maxCount), jetstream.FetchMaxWait(blockDuration))
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch messages: %w", err)
	}

	var tasks []domain.Task
	var lastMsgId string
	for msg := range msgs.Messages() {
		var task domain.Task

		if err := json.Unmarshal(msg.Data(), &task); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal task: %w", err)
		}
		tasks = append(tasks, task)

		meta, err := msg.Metadata()
		if err != nil {
			return nil, "", fmt.Errorf("failed to get message metadata: %w", err)
		}
		lastMsgId = fmt.Sprintf("%d", meta.Sequence.Stream)

		msg.Ack()
	}

	if len(tasks) == 0 {
		return tasks, streamMessageStartId, nil
	}
	return tasks, lastMsgId, nil
}
