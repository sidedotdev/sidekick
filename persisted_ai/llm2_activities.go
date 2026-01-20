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

// StreamInput is the activity input for LLM streaming that carries chat history for hydration.
type StreamInput struct {
	Options      llm2.Options
	WorkspaceId  string
	FlowId       string
	FlowActionId string
}

// Llm2Activities provides LLM streaming activities using the llm2 package types.
type Llm2Activities struct {
	Streamer srv.Streamer
	Storage  common.KeyValueStorage
}

// Stream executes an LLM streaming request and sends events to the flow event stream.
// It hydrates the chat history from storage and derives messages at runtime.
func (la *Llm2Activities) Stream(ctx context.Context, input StreamInput) (*llm2.MessageResponse, error) {
	if input.Options.Params.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in StreamInput.Options.Params")
	}

	if err := input.Options.Params.ChatHistory.Hydrate(ctx, la.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	eventChan := make(chan llm2.Event, 10)

	go func() {
		defer func() {
			if input.FlowActionId == "" {
				return
			}
			err := la.Streamer.EndFlowEventStream(context.Background(), input.WorkspaceId, input.FlowId, input.FlowActionId)
			if err != nil {
				log.Error().Err(err).Msg("failed to mark the end of the flow event stream")
			}
		}()

		for event := range eventChan {
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, event)
			}
			if input.FlowActionId == "" {
				continue
			}

			// Convert llm2.Event to domain flow event
			flowEvent := convertLlm2EventToFlowEvent(event, input.FlowActionId)
			if flowEvent == nil {
				continue
			}

			err := la.Streamer.AddFlowEvent(context.Background(), input.WorkspaceId, input.FlowId, flowEvent)
			if err != nil {
				log.Error().Err(err).
					Str("workspaceId", input.WorkspaceId).
					Str("flowId", input.FlowId).
					Str("flowActionId", input.FlowActionId).
					Msg("failed to add llm2 event to flow event stream")
			}
		}
	}()

	provider, err := getLlm2Provider(input.Options.Params.ModelConfig)
	if err != nil {
		close(eventChan)
		log.Error().Err(err).Msg("failed to get llm2 provider")
		return nil, err
	}

	response, err := provider.Stream(ctx, input.Options, eventChan)
	close(eventChan)

	if response != nil {
		response.Provider = input.Options.Params.ModelConfig.Provider
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
	case "OPENAI":
		return llm2.OpenAIResponsesProvider{}, nil
	case "ANTHROPIC":
		return llm2.AnthropicResponsesProvider{}, nil
	default:
		return nil, fmt.Errorf("unsupported llm2 provider: %s", config.Provider)
	}
}
