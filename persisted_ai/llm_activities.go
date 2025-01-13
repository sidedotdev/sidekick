package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv"
	"strings"

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

			contentBuilder := strings.Builder{}
			if delta.Content != "" {
				//fmt.Print(delta.Content)
				contentBuilder.WriteString(delta.Content)
			}
			if delta.ToolCalls != nil {
				for _, toolCall := range delta.ToolCalls {
					if toolCall.Name != "" {
						//fmt.Printf("toolName = %s\n", toolCall.Name)
						contentBuilder.WriteString(fmt.Sprintf("toolName = %s\n", toolCall.Name))
					}
					if toolCall.Arguments != "" {
						//fmt.Print(toolCall.Arguments)
						contentBuilder.WriteString(toolCall.Arguments)
					}
				}
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

	toolChatter, err := getToolChatter(options.Params.Provider, options.Params.Model)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get tool chatter")
		return nil, err
	}
	return toolChatter.ChatStream(ctx, options.ToolChatOptions, deltaChan)
}

func getToolChatter(provider llm.ToolChatProvider, model string) (llm.ToolChatter, error) {
	switch provider {
	case llm.UnspecifiedToolChatProvider:
		if strings.HasPrefix(model, "gpt") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") {
			return llm.OpenaiToolChat{}, nil
		}

		if strings.HasPrefix(model, "claude-") {
			return llm.AnthropicToolChat{}, nil
		}

		if model == "" {
			return nil, errors.New("unspecified tool chat provider")
		}

		return nil, fmt.Errorf("unsupported tool chat model: %v", model)

	case llm.OpenaiToolChatProvider:
		return llm.OpenaiToolChat{}, nil
	case llm.AnthropicToolChatProvider:
		return llm.AnthropicToolChat{}, nil
	default:
		return nil, fmt.Errorf("unsupported tool chat provider: %s", provider)
	}
}
