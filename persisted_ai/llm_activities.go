package persisted_ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv"
	"strings"

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
			// Mark the end of the Redis stream
			err := la.Streamer.EndFlowEventStream(context.Background(), options.WorkspaceId, options.FlowId, options.FlowActionId)
			if err != nil {
				log.Printf("failed to mark the end of the Redis stream: %v", err)
			}
		}()
		for delta := range deltaChan {
			// Record heartbeat only if we're in an activity context to avoid panic.
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, delta)
			}

			contentBuilder := strings.Builder{}
			if delta.Content != "" {
				fmt.Print(delta.Content)
				contentBuilder.WriteString(delta.Content)
			}
			if delta.ToolCalls != nil {
				for _, toolCall := range delta.ToolCalls {
					if toolCall.Name != "" {
						fmt.Printf("toolName = %s\n", toolCall.Name)
						contentBuilder.WriteString(fmt.Sprintf("toolName = %s\n", toolCall.Name))
					}
					if toolCall.Arguments != "" {
						fmt.Print(toolCall.Arguments)
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
				log.Printf("failed to add chat message delta flow event to Redis stream: %v", err)
			}
		}

	}()

	toolChatter, err := getToolChatter(options.Params.ModelConfig)
	if err != nil {
		return nil, err
	}
	return toolChatter.ChatStream(ctx, options.ToolChatOptions, deltaChan)
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