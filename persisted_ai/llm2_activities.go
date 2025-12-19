package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm2"
	"sidekick/srv"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

// StreamOptions combines llm2.Options with workflow/flow context identifiers.
type StreamOptions struct {
	llm2.Options
	WorkspaceId  string
	FlowId       string
	FlowActionId string
}

// Llm2Activities provides LLM streaming activities using the llm2 package types.
type Llm2Activities struct {
	Streamer srv.Streamer
}

// Stream executes an LLM streaming request and sends events to the flow event stream.
func (la *Llm2Activities) Stream(ctx context.Context, options StreamOptions) (*llm2.MessageResponse, error) {
	eventChan := make(chan llm2.Event, 10)

	go func() {
		defer func() {
			if options.FlowActionId == "" {
				return
			}
			err := la.Streamer.EndFlowEventStream(context.Background(), options.WorkspaceId, options.FlowId, options.FlowActionId)
			if err != nil {
				log.Error().Err(err).Msg("failed to mark the end of the flow event stream")
			}
		}()

		for event := range eventChan {
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, event)
			}
			if options.FlowActionId == "" {
				continue
			}

			// Convert llm2.Event to domain flow event
			flowEvent := convertLlm2EventToFlowEvent(event, options.FlowActionId)
			if flowEvent == nil {
				continue
			}

			err := la.Streamer.AddFlowEvent(context.Background(), options.WorkspaceId, options.FlowId, flowEvent)
			if err != nil {
				log.Error().Err(err).
					Str("workspaceId", options.WorkspaceId).
					Str("flowId", options.FlowId).
					Str("flowActionId", options.FlowActionId).
					Msg("failed to add llm2 event to flow event stream")
			}
		}
	}()

	provider, err := getLlm2Provider(options.Params.ModelConfig)
	if err != nil {
		close(eventChan)
		log.Error().Err(err).Msg("failed to get llm2 provider")
		return nil, err
	}

	response, err := provider.Stream(ctx, options.Options, eventChan)
	close(eventChan)

	if response != nil {
		response.Provider = options.Params.ModelConfig.Provider
	}

	return response, err
}

// convertLlm2EventToFlowEvent converts an llm2.Event to a domain.FlowEvent for streaming.
func convertLlm2EventToFlowEvent(event llm2.Event, flowActionId string) domain.FlowEvent {
	switch event.Type {
	case llm2.EventTextDelta:
		return domain.ChatMessageDeltaEvent{
			EventType:    domain.ChatMessageDeltaEventType,
			FlowActionId: flowActionId,
			ChatMessageDelta: common.ChatMessageDelta{
				Content: event.Delta,
			},
		}
	case llm2.EventSummaryTextDelta:
		return domain.ProgressTextEvent{
			EventType: domain.ProgressTextEventType,
			ParentId:  flowActionId,
			Text:      event.Delta,
		}
	default:
		return nil
	}
}

// getLlm2Provider returns the appropriate llm2.Provider based on the model configuration.
func getLlm2Provider(config common.ModelConfig) (llm2.Provider, error) {
	providerName := config.NormalizedProviderName()

	switch providerName {
	case "openai":
		return llm2.OpenAIResponsesProvider{}, nil
	case "anthropic":
		return llm2.AnthropicResponsesProvider{}, nil
	default:
		return nil, fmt.Errorf("unsupported llm2 provider: %s", config.Provider)
	}
}
