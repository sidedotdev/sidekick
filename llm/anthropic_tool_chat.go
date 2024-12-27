package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog/log"
)

const AnthropicDefaultModel = "claude-3-sonnet-20240229"
const AnthropicDefaultLongContextModel = "claude-3-opus-20240229"
const AnthropicApiKeySecretName = "ANTHROPIC_API_KEY"

type AnthropicToolChat struct{}

func (AnthropicToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error) {
	token, err := options.Secrets.SecretManager.GetSecret(AnthropicApiKeySecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic API key: %w", err)
	}

	client := anthropic.NewClient(
		option.WithAPIKey(token),
	)

	messages, err := anthropicFromChatMessages(options.Params.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat messages: %w", err)
	}

	tools, err := anthropicFromTools(options.Params.Tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	model := options.Params.Model
	if model == "" {
		model = AnthropicDefaultModel
	}

	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(model),
		MaxTokens: anthropic.Int(1024),
		Messages:  anthropic.F(messages),
		Tools:     anthropic.F(tools),
	})

	var finalMessage anthropic.Message
	for stream.Next() {
		event := stream.Current()
		err := finalMessage.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate message: %w", err)
		}

		delta, err := anthropicToChatMessageDelta(event)
		if err != nil {
			return nil, fmt.Errorf("failed to convert delta: %w", err)
		}

		if delta != nil {
			log.Trace().Interface("delta", delta).Msg("Received delta from Anthropic API")
			deltaChan <- *delta
		}
	}

	if stream.Err() != nil {
		return nil, fmt.Errorf("stream error: %w", stream.Err())
	}

	response, err := anthropicToChatMessageResponse(finalMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to convert response: %w", err)
	}

	return response, nil
}

func anthropicFromChatMessages(messages []ChatMessage) ([]anthropic.MessageParam, error) {
	var result []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion

		if msg.Content != "" {
			blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
		}
		for _, toolCall := range msg.ToolCalls {
			/*
				var args json.Marshaler
				args = json.RawMessage(toolCall.Arguments)
				blocks = append(blocks, anthropic.ContentBlockParam{
					Type:  anthropic.F(anthropic.ContentBlockParamTypeToolUse),
					Name:  anthropic.F(toolCall.Name),
					Input: anthropic.F(args.(any)),
				})
			*/
			toolUseBlock := anthropic.NewToolUseBlockParam(msg.ToolCallId, toolCall.Name, json.RawMessage(toolCall.Arguments))
			blocks = append(blocks, toolUseBlock)
		}
		result = append(result, anthropic.MessageParam{
			Role:    anthropic.F(anthropicFromChatMessageRole(msg.Role)),
			Content: anthropic.F(blocks),
		})
	}
	return result, nil
}

func anthropicFromChatMessageRole(role ChatMessageRole) anthropic.MessageParamRole {
	switch role {
	case ChatMessageRoleSystem:
		// anthropic doesn't have a system role
		return anthropic.MessageParamRoleAssistant
	case ChatMessageRoleUser:
		return anthropic.MessageParamRoleUser
	case ChatMessageRoleAssistant:
		return anthropic.MessageParamRoleAssistant
	default:
		panic(fmt.Sprintf("unknown role: %s", role))
	}
}

func anthropicFromTools(tools []*Tool) ([]anthropic.ToolParam, error) {
	var result []anthropic.ToolParam
	for _, tool := range tools {
		schema, err := json.Marshal(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tool parameters: %w", err)
		}
		result = append(result, anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F[interface{}](json.RawMessage(schema)),
		})
	}
	return result, nil
}

func anthropicToChatMessageDelta(event anthropic.MessageStreamEvent) (*ChatMessageDelta, error) {
	switch e := event.AsUnion().(type) {
	case *anthropic.ContentBlockDeltaEvent:
		return &ChatMessageDelta{
			Content: e.Delta.Text,
		}, nil
	case *anthropic.ContentBlockStartEvent:
		return &ChatMessageDelta{
			ToolCalls: []ToolCall{{
				Name:      e.ContentBlock.Name,
				Arguments: "",
			}},
		}, nil
	case *anthropic.ContentBlockStopEvent:
		// Handle tool call completion if needed
	}
	return nil, nil
}

func anthropicToChatMessageResponse(message anthropic.Message) (*ChatMessageResponse, error) {
	response := &ChatMessageResponse{
		ChatMessage: ChatMessage{
			Role:    ChatMessageRoleAssistant,
			Content: "",
		},
		Id:         message.ID,
		StopReason: string(message.StopReason),
		Usage: Usage{
			InputTokens:  int(message.Usage.InputTokens),
			OutputTokens: int(message.Usage.OutputTokens),
		},
		Model:    message.Model,
		Provider: common.AnthropicChatProvider,
	}

	for _, block := range message.Content {
		switch block.Type {
		case anthropic.ContentBlockTypeText:
			response.Content += block.Text
		case anthropic.ContentBlockTypeToolUse:
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}

	return response, nil
}
