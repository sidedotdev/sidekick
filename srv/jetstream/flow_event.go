package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sync"

	"github.com/nats-io/nats.go/jetstream"
)

var _ domain.FlowEventStreamer = (*Streamer)(nil)

func (s *Streamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	data, err := json.Marshal(flowEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal flow event: %w", err)
	}

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

func (s *Streamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan domain.FlowEventSubscription) (<-chan domain.FlowEvent, <-chan error) {
	eventCh := make(chan domain.FlowEvent)
	errCh := make(chan error, 1)

	eventParentIdSet := make(map[string]bool)
	go func() {
		defer close(eventCh)
		defer close(errCh)

		wg := &sync.WaitGroup{}
	outer:
		for {
			select {
			case <-ctx.Done():
				break outer
			case subscription, ok := <-subscriptionCh:
				if !ok {
					break outer
				}
				if eventParentIdSet[subscription.ParentId] {
					continue
				}
				subject := fmt.Sprintf("flow_events.%s.%s", workspaceId, subscription.ParentId)

				// default to starting from the start of the stream for flow events
				streamMessageStartId := subscription.StreamMessageStartId
				if streamMessageStartId == "" {
					streamMessageStartId = "0"
				}

				consumer, err := s.createConsumer(ctx, subject, streamMessageStartId)
				if err != nil {
					errCh <- fmt.Errorf("failed to create consumer for event parent ID %s: %w", subscription.ParentId, err)
					break outer
				}

				wg.Add(1)
				go s.consumeFlowEvents(ctx, consumer, eventCh, errCh, wg)
				eventParentIdSet[subscription.ParentId] = true
			}
		}
		// ensure all consumers are stopped before closing channels
		wg.Wait()
	}()

	return eventCh, errCh
}

func (s *Streamer) consumeFlowEvents(ctx context.Context, consumer jetstream.Consumer, eventCh chan<- domain.FlowEvent, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	var consContext jetstream.ConsumeContext
	consContext, err := consumer.Consume(func(msg jetstream.Msg) {
		event, err := domain.UnmarshalFlowEvent(msg.Data())
		if err != nil {
			errCh <- fmt.Errorf("failed to unmarshal flow event: %w", err)
			return
		}

		if event == nil {
			// This can happen with empty messages, just ignore them
			msg.Ack()
			return
		}

		eventCh <- event // FIXME this can panic if eventCh is closed
		if _, ok := event.(domain.EndStreamEvent); ok {
			consContext.Stop()
		}
		msg.Ack()
	})
	if err != nil {
		errCh <- fmt.Errorf("failed to consume messages: %w", err)
		return
	}

	select {
	case <-consContext.Closed():
	case <-ctx.Done():
		// ensure the consumer is closed before calling wg.Done()
		consContext.Stop()
		<-consContext.Closed()
	}
}
