package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

var _ domain.FlowEventStreamer = (*Streamer)(nil)

func (s *Streamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	data, err := json.Marshal(flowEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal flow event: %w", err)
	}
	fmt.Printf("adding flow event: %s\n", data)

	subject := fmt.Sprintf("flow_events.%s.%s", workspaceId, flowEvent.GetParentId())
	_, err = s.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish flow event: %w", err)
	}

	return nil
}

func (s *Streamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	data, err := json.Marshal(domain.EndStreamEvent{
		EventType: domain.EndStreamEventType,
		ParentId:  eventStreamParentId,
	})
	if err != nil {
		return fmt.Errorf("failed to serialize flow event: %v", err)
	}

	subject := fmt.Sprintf("flow_events.%s.%s", workspaceId, eventStreamParentId)
	_, err = s.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish flow event: %w", err)
	}

	return nil
}

func (s *Streamer) GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]domain.FlowEvent, map[string]string, error) {
	if maxCount == 0 {
		maxCount = 100
	}

	newStreamKeys := make(map[string]string)
	var events []domain.FlowEvent

	// Process each flow ID stream separately
	for streamKey, startId := range streamKeys {
		parts := strings.Split(streamKey, ":")
		if len(parts) != 4 || parts[0] != workspaceId || parts[2] != "stream" {
			return nil, nil, fmt.Errorf("invalid stream key format: %s", streamKey)
		}
		flowEventParentId := parts[3]
		subject := fmt.Sprintf("flow_events.%s.%s", workspaceId, flowEventParentId)

		// default to starting from the beginning for flow events
		if startId == "" {
			startId = "0"
		} else if startId == "$" {
			return nil, nil, fmt.Errorf("$ not supported for flow events, but received for flow %s", flowEventParentId)
		}

		var deliveryPolicy jetstream.DeliverPolicy
		var startTime *time.Time
		var startSeq uint64

		if startId == "0" {
			deliveryPolicy = jetstream.DeliverAllPolicy
		} else {
			deliveryPolicy = jetstream.DeliverByStartSequencePolicy
			var err error
			startSeq, err = strconv.ParseUint(startId, 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid stream message start id for flow %s: %w", flowEventParentId, err)
			}
		}

		consumer, err := s.js.OrderedConsumer(ctx, PersistentStreamName, jetstream.OrderedConsumerConfig{
			FilterSubjects:    []string{subject},
			InactiveThreshold: 5 * time.Minute,
			DeliverPolicy:     deliveryPolicy,
			OptStartSeq:       startSeq,
			OptStartTime:      startTime,
		})
		if err != nil && err != jetstream.ErrConsumerNameAlreadyInUse {
			return nil, nil, fmt.Errorf("failed to create consumer for flow %s: %w", flowEventParentId, err)
		}

		// Pull messages
		waitPerKey := blockDuration / time.Duration(len(streamKeys))
		msgs, err := consumer.Fetch(int(maxCount), jetstream.FetchMaxWait(waitPerKey))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch messages for flow %s: %w", flowEventParentId, err)
		}

		var lastSequence uint64
		for msg := range msgs.Messages() {
			fmt.Printf("got flow event message: %s\n", string(msg.Data()))
			event, err := domain.UnmarshalFlowEvent(msg.Data())
			if err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal flow event: %w", err)
			}
			events = append(events, event)
			msg.Ack()

			meta, err := msg.Metadata()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get message metadata: %w", err)
			}
			lastSequence = meta.Sequence.Stream
		}

		if lastSequence > 0 {
			newStreamKeys[streamKey] = fmt.Sprintf("%d", lastSequence+1)
		} else {
			newStreamKeys[streamKey] = startId
		}
	}

	return events, newStreamKeys, nil
}
