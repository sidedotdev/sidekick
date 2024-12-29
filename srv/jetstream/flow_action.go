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

// Ensure Streamer implements FlowActionStreamer
var _ domain.FlowActionStreamer = (*Streamer)(nil)

func (s *Streamer) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
	data, err := json.Marshal(flowAction)
	if err != nil {
		return fmt.Errorf("failed to marshal flow action: %w", err)
	}

	subject := fmt.Sprintf("flow_actions.changes.%s.%s", flowAction.WorkspaceId, flowAction.FlowId)
	_, err = s.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish flow action change: %w", err)
	}

	return nil
}

func (s *Streamer) GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.FlowAction, string, error) {
	if maxCount == 0 {
		maxCount = 100
	}
	// default to starting from the start of the stream for flow action changes
	if streamMessageStartId == "" {
		streamMessageStartId = "0"
	}

	subject := fmt.Sprintf("flow_actions.changes.%s.%s", workspaceId, flowId)

	var deliveryPolicy jetstream.DeliverPolicy
	var startTime *time.Time
	var startSeq uint64
	if streamMessageStartId == "0" {
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
	}

	consumer, err := s.js.OrderedConsumer(ctx, PersistentStreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects:    []string{subject},
		InactiveThreshold: 5 * time.Minute,
		DeliverPolicy:     deliveryPolicy,
		OptStartSeq:       startSeq,
		OptStartTime:      startTime,
	})
	if err != nil {
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

	var flowActions []domain.FlowAction
	var lastSequence uint64

	for msg := range msgs.Messages() {
		var flowAction domain.FlowAction
		if err := json.Unmarshal(msg.Data(), &flowAction); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal flow action: %w", err)
		}

		// Handle end message
		if flowAction.Id == "end" {
			msg.Ack()
			return flowActions, "end", nil
		}

		flowActions = append(flowActions, flowAction)
		msg.Ack()

		metadata, err := msg.Metadata()
		if err != nil {
			return nil, "", fmt.Errorf("failed to get message metadata: %w", err)
		}
		lastSequence = metadata.Sequence.Stream
	}

	if len(flowActions) == 0 {
		return nil, streamMessageStartId, nil
	}

	return flowActions, fmt.Sprintf("%d", lastSequence+1), nil
}
func (s *Streamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	panic("StreamFlowActionChanges is not yet implemented")
}
