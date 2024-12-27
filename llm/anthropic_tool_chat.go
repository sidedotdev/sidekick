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

const AnthropicDefaultModel = anthropic.ModelClaude3_5SonnetLatest
const AnthropicDefaultLongContextModel = anthropic.ModelClaude3_5SonnetLatest
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

	var temperature float32 = defaultTemperature
	if options.Params.Temperature != nil {
		temperature = *options.Params.Temperature
	}

	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Temperature: anthropic.F(float64(temperature)),
		Model:       anthropic.F(model),
		MaxTokens:   anthropic.Int(4000),
		Messages:    anthropic.F(messages),
		Tools:       anthropic.F(tools),
	})

	var finalMessage anthropic.Message
	for stream.Next() {
		event := stream.Current()
		log.Trace().Interface("event", event).Msg("Received streamed event from Anthropic API")

		err := finalMessage.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate message: %w", err)
		}

		switch event := event.AsUnion().(type) {
		case anthropic.ContentBlockStartEvent:
			deltaChan <- anthropicContentStartToChatMessageDelta(event.ContentBlock)
		case anthropic.ContentBlockDeltaEvent:
			if len(finalMessage.Content) == 0 {
				return nil, fmt.Errorf("anthropic tool chat failure: received event of type %s but there was no content block", event.Type)
			}
			deltaChan <- anthropicToChatMessageDelta(event.Delta)
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

func anthropicToChatMessageDelta(eventDelta anthropic.ContentBlockDeltaEventDelta) ChatMessageDelta {
	outDelta := ChatMessageDelta{Role: ChatMessageRoleAssistant}

	switch delta := eventDelta.AsUnion().(type) {
	case anthropic.TextDelta:
		outDelta.Content = delta.Text
	case anthropic.InputJSONDelta:
		outDelta.ToolCalls = append(outDelta.ToolCalls, ToolCall{
			Arguments: delta.PartialJSON,
		})
	default:
		panic(fmt.Sprintf("unsupported delta type: %v", eventDelta.Type))
	}

	return outDelta
}

func anthropicContentStartToChatMessageDelta(contentBlock anthropic.ContentBlockStartEventContentBlock) ChatMessageDelta {
	switch contentBlock.Type {
	case "text":
		return ChatMessageDelta{
			Role:    ChatMessageRoleAssistant,
			Content: contentBlock.Text,
		}
	case "tool_use":
		return ChatMessageDelta{
			Role: ChatMessageRoleAssistant,
			ToolCalls: []ToolCall{
				{
					Id:        contentBlock.ID,
					Name:      contentBlock.Name,
					Arguments: string(contentBlock.Input),
				},
			},
		}
	default:
		panic(fmt.Sprintf("unsupported content block type: %s", contentBlock.Type))
	}
}

func anthropicFromChatMessages(messages []ChatMessage) ([]anthropic.MessageParam, error) {
	var result []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion

		if msg.Content != "" {
			if msg.Role == ChatMessageRoleTool {
				blocks = append(blocks, anthropic.NewToolResultBlock(msg.ToolCallId, msg.Content, msg.IsError))
			} else {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
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
	case ChatMessageRoleSystem, ChatMessageRoleUser, ChatMessageRoleTool:
		// anthropic doesn't have a system role nor a tool role
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

func anthropicToChatMessageResponse(message anthropic.Message) (*ChatMessageResponse, error) {
	response := &ChatMessageResponse{
		ChatMessage: ChatMessage{
			Role:    ChatMessageRoleAssistant,
			Content: "",
		},
		Id:           message.ID,
		StopReason:   string(message.StopReason),
		StopSequence: message.StopSequence,
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
				Id:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}

	return response, nil
}
