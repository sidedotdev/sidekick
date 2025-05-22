package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

type ChatStreamOptions struct {
	llm.ToolChatOptions
	WorkspaceId  string
	FlowId       string
	FlowActionId string
}

type LlmActivities struct {
	Streamer srv.Streamer
}

func (la *LlmActivities) ChatStream(ctx context.Context, options ChatStreamOptions) (*llm.ChatMessageResponse, error) {
	deltaChan := make(chan llm.ChatMessageDelta, 10)
	defer close(deltaChan)

	go func() {
		defer func() {
			// Mark the end of the stream
			err := la.Streamer.EndFlowEventStream(context.Background(), options.WorkspaceId, options.FlowId, options.FlowActionId)
			if err != nil {
				log.Error().Err(err).Msg("failed to mark the end of the flow event stream")
			}
		}()
		for delta := range deltaChan {
			// Record heartbeat only if we're in an activity context to avoid panic.
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, delta)
			}
			flowEventDelta := domain.ChatMessageDeltaEvent{
				FlowActionId:     options.FlowActionId,
				EventType:        domain.ChatMessageDeltaEventType,
				ChatMessageDelta: delta,
			}
			err := la.Streamer.AddFlowEvent(context.Background(), options.WorkspaceId, options.FlowId, flowEventDelta)
			if err != nil {
				log.Error().Err(err).Msg("failed to add chat message delta flow event to stream")
			}
		}

	}()

	toolChatter, err := getToolChatter(options.Params.ModelConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get tool chatter")
		return nil, err
	}

	// First attempt
	progressChan := make(chan llm.ProgressInfo, 10)
	defer close(progressChan)

	go func() {
		for progress := range progressChan {
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, progress)
			}
			progressEvent := domain.ProgressTextEvent{
				ParentId:  options.FlowActionId,
				EventType: domain.ProgressTextEventType,
				Text:      progress.Title,
				Details:   progress.Details,
			}
			err := la.Streamer.AddFlowEvent(context.Background(), options.WorkspaceId, options.FlowId, progressEvent)
			if err != nil {
				log.Error().Err(err).Msg("failed to add progress text event to stream")
			}
		}
	}()

	response, err := toolChatter.ChatStream(ctx, options.ToolChatOptions, deltaChan, progressChan)
	if err != nil {
		return response, err
	}
	response.Provider = options.Params.ModelConfig.Provider

	// Check for empty response
	if len(response.Content) == 0 && len(response.ToolCalls) == 0 {
		log.Debug().Msg("Received empty response, attempting retry with modified prompt")
		return retryChatStreamOnEmptyResponse(ctx, options.ToolChatOptions, response, toolChatter, deltaChan, progressChan)
	}

	return response, nil
}

func retryChatStreamOnEmptyResponse(
	ctx context.Context,
	chatOptions llm.ToolChatOptions,
	originalResponse *llm.ChatMessageResponse,
	toolChatter llm.ToolChatter,
	deltaChan chan llm.ChatMessageDelta,
	progressChan chan llm.ProgressInfo,
) (*llm.ChatMessageResponse, error) {
	// this shows what's going on to the user in the streaming UI
	deltaChan <- llm.ChatMessageDelta{
		Role: llm.ChatMessageRoleAssistant,
		Content: `
----------------------------------------------------------------------------
Sidekick: got an unexpected empty response. Retrying with modified prompt...
----------------------------------------------------------------------------

`,
	}

	// Create retry options with additional messages
	chatOptions.Params.Messages = append(chatOptions.Params.Messages,
		llm.ChatMessage{
			Role:    llm.ChatMessageRoleAssistant,
			Content: "(error: unexpected empty response)",
		},
		llm.ChatMessage{
			Role:    llm.ChatMessageRoleSystem,
			Content: "Please provide a non-empty response",
		},
	)

	// Second attempt
	retryResponse, err := toolChatter.ChatStream(ctx, chatOptions, deltaChan, progressChan)
	if err != nil {
		retryResponse.Provider = chatOptions.Params.ModelConfig.Provider
		return retryResponse, err
	}

	// Sum up token usage from both attempts
	retryResponse.Usage.InputTokens += originalResponse.Usage.InputTokens
	retryResponse.Usage.OutputTokens += originalResponse.Usage.OutputTokens

	// If retry also returns empty response, replace content to be not actually
	// empty - so as to prevent issues later. but don't consider this an actual
	// error: most errors are expected to be retried, and we don't have
	// non-retryable errors set up with temporal yet, and this complicates what
	// callers will have to do with these responses, requiring special handling
	// for specific classes of error. instead, we'll let the upstream caller
	// just process this as normal, and hope that their processing causes a
	// change in the followup LLM calls, given this is a recoverable situation
	// on retry to the LLM model with some new input.
	if len(retryResponse.Content) == 0 && len(retryResponse.ToolCalls) == 0 {
		retryResponse.Content = "(error: unexpected empty response)"
	}

	retryResponse.Provider = chatOptions.Params.ModelConfig.Provider
	return retryResponse, nil
}

func getToolChatter(config common.ModelConfig) (llm.ToolChatter, error) {
	providerType, err := getProviderType(config.Provider)
	if err != nil {
		return nil, err
	}

	switch providerType {
	case llm.OpenaiToolChatProviderType:
		return llm.OpenaiToolChat{}, nil
	case llm.OpenaiCompatibleToolChatProviderType:
		localConfig, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
		if err != nil {
			return nil, fmt.Errorf("failed to load local config: %w", err)
		}
		for _, p := range localConfig.Providers {
			if p.Type == string(providerType) {
				return llm.OpenaiToolChat{
					BaseURL:      p.BaseURL,
					DefaultModel: p.DefaultLLM,
				}, nil
			}
		}
		return nil, fmt.Errorf("configuration not found for provider named: %s", config.Provider)
	case llm.AnthropicToolChatProviderType:
		return llm.AnthropicToolChat{}, nil
	case llm.GoogleToolChatProviderType:
		return llm.GoogleToolChat{}, nil
	case llm.UnspecifiedToolChatProviderType:
		return nil, errors.New("tool chat provider was not specified")

	default:
		return nil, fmt.Errorf("unsupported tool chat provider type: %s", providerType)
	}
}

func getProviderType(s string) (llm.ToolChatProviderType, error) {

	switch s {
	case "openai":
		return llm.OpenaiToolChatProviderType, nil
	case "anthropic":
		return llm.AnthropicToolChatProviderType, nil
	case "google":
		return llm.GoogleToolChatProviderType, nil
	case "mock":
		return llm.ToolChatProviderType("mock"), nil
	}

	// TODO first try workspace config to determine provider type, then fallback to local config
	localConfig, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	if err != nil {
		return llm.UnspecifiedToolChatProviderType, fmt.Errorf("failed to load local config: %w", err)
	}

	for _, provider := range localConfig.Providers {
		if provider.Name == s {
			return llm.ToolChatProviderType(provider.Type), nil
		}
	}

	return llm.UnspecifiedToolChatProviderType, fmt.Errorf("unknown provider: %s", s)
}
