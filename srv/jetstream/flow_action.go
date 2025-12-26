package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"

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

func (s *Streamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	// default to new-only streaming for flow action changes
	if streamMessageStartId == "" {
		streamMessageStartId = "$"
	}

	flowActionChan := make(chan domain.FlowAction)
	errChan := make(chan error, 1)

	go func() {
		defer close(flowActionChan)
		defer close(errChan)

		subject := fmt.Sprintf("flow_actions.changes.%s.%s", workspaceId, flowId)

		consumer, err := s.createConsumer(ctx, subject, streamMessageStartId)
		if err != nil {
			errChan <- fmt.Errorf("failed to create consumer: %w", err)
			return
		}

		var consContext jetstream.ConsumeContext
		consContext, err = consumer.Consume(func(msg jetstream.Msg) {
			var flowAction domain.FlowAction
			if err := json.Unmarshal(msg.Data(), &flowAction); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal flow action: %w", err)
				return
			}
			select {
			case flowActionChan <- flowAction:
				if flowAction.Id == "end" {
					fmt.Printf("Received end message\n")
					consContext.Stop()
				}
				msg.Ack()
			case <-ctx.Done():
				return
			}
		})
		if err != nil {
			errChan <- fmt.Errorf("failed to create consume context: %w", err)
			return
		}

		select {
		case <-consContext.Closed():
		case <-ctx.Done():
			consContext.Stop()
			<-consContext.Closed()
		}
	}()

	return flowActionChan, errChan
}
