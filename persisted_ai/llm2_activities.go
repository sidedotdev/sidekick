package persisted_ai

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

// StreamInput is the activity input for LLM streaming that carries chat history for hydration.
// It separates chat history and secrets from pure LLM config (Options) so that
// providers never see storage-aware types.
type StreamInput struct {
	Options      llm2.Options
	Secrets      secret_manager.SecretManagerContainer
	ChatHistory  *ChatHistoryContainer
	WorkspaceId  string
	FlowId       string
	FlowActionId string
	Providers    []common.ModelProviderPublicConfig
}

// ActionParams returns action parameters for flow action tracking.
func (si StreamInput) ActionParams() map[string]any {
	params, err := utils.StructToMap(si.Options.Params)
	if err != nil {
		params = map[string]any{}
	}
	params["messages"] = si.ChatHistory
	params["secretManagerType"] = si.Secrets.GetType()
	return params
}

// Llm2Activities provides LLM streaming activities using the llm2 package types.
type Llm2Activities struct {
	Streamer srv.Streamer
	Storage  common.KeyValueStorage
}

// Stream executes an LLM streaming request and sends events to the flow event stream.
// It hydrates the chat history from storage and derives messages at runtime.
func (la *Llm2Activities) Stream(ctx context.Context, input StreamInput) (*llm2.MessageResponse, error) {
	if input.ChatHistory == nil {
		return nil, fmt.Errorf("ChatHistory is required in StreamInput")
	}

	if err := input.ChatHistory.Hydrate(ctx, la.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	// Background heartbeat keeps the activity alive during long provider calls
	// where no events are emitted (e.g. extended thinking).
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if activity.IsActivity(ctx) {
					activity.RecordHeartbeat(ctx, nil)
				}
			}
		}
	}()

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

	provider, err := getLlm2Provider(input.Options.Params.ModelConfig, input.Providers)
	if err != nil {
		close(eventChan)
		log.Error().Err(err).Msg("failed to get llm2 provider")
		return nil, err
	}

	llm2History, ok := input.ChatHistory.History.(*Llm2ChatHistory)
	if !ok {
		close(eventChan)
		return nil, fmt.Errorf("Stream requires Llm2ChatHistory, got %T", input.ChatHistory.History)
	}

	request := llm2.StreamRequest{
		Messages:      llm2History.Llm2Messages(),
		Options:       input.Options,
		SecretManager: input.Secrets.SecretManager,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if response != nil {
		response.Provider = input.Options.Params.ModelConfig.Provider
		response.Output.SanitizeToolNames()
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
func getLlm2Provider(config common.ModelConfig, providers []common.ModelProviderPublicConfig) (llm2.Provider, error) {
	providerType, err := getProviderType(config.Provider)
	if err != nil {
		return nil, err
	}

	switch providerType {
	case llm.OpenaiToolChatProviderType:
		return llm2.OpenAIResponsesProvider{}, nil
	case llm.OpenaiCompatibleToolChatProviderType:
		for _, p := range providers {
			if p.Type == string(providerType) && p.Name == config.Provider {
				return llm2.OpenAIProvider{
					BaseURL:      p.BaseURL,
					DefaultModel: p.DefaultLLM,
				}, nil
			}
		}
		return nil, fmt.Errorf("configuration not found for provider named: %s", config.Provider)
	case llm.OpenaiResponsesCompatibleToolChatProviderType:
		for _, p := range providers {
			if p.Type == string(providerType) && p.Name == config.Provider {
				return llm2.OpenAIResponsesProvider{
					BaseURL:      p.BaseURL,
					DefaultModel: p.DefaultLLM,
				}, nil
			}
		}
		return nil, fmt.Errorf("configuration not found for provider named: %s", config.Provider)
	case llm.AnthropicToolChatProviderType:
		return llm2.AnthropicProvider{}, nil
	case llm.GoogleToolChatProviderType:
		return llm2.GoogleProvider{}, nil
	case llm.UnspecifiedToolChatProviderType:
		return nil, fmt.Errorf("llm2 provider was not specified")
	default:
		return nil, fmt.Errorf("unsupported llm2 provider: %s", config.Provider)
	}
}
