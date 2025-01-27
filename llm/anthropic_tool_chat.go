package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
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

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	client := anthropic.NewClient(
		//option.WithBaseURL("https://api.anthropic.com"),
		option.WithAPIKey(token),
		option.WithHTTPClient(httpClient), // NOTE: WithRequestTimeout was causing failure after a single token
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
	startedBlocks := 0
	stoppedBlocks := 0
	for stream.Next() {
		event := stream.Current()
		if activity.IsActivity(ctx) {
			activity.RecordHeartbeat(ctx, event)
		}
		log.Trace().Interface("event", event).Msg("Received streamed event from Anthropic API")

		err := finalMessage.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate message: %w", err)
		}

		switch event := event.AsUnion().(type) {
		case anthropic.ContentBlockStartEvent:
			deltaChan <- anthropicContentStartToChatMessageDelta(event.ContentBlock)
			startedBlocks++
		case anthropic.ContentBlockDeltaEvent:
			if len(finalMessage.Content) == 0 {
				return nil, fmt.Errorf("anthropic tool chat failure: received event of type %s but there was no content block", event.Type)
			}
			deltaChan <- anthropicToChatMessageDelta(event.Delta)
		case anthropic.ContentBlockStopEvent:
			stoppedBlocks++
		}
	}

	if stream.Err() != nil {
		log.Error().Err(stream.Err()).Msg("Anthropic tool chat stream error")
		return nil, fmt.Errorf("stream error: %w", stream.Err())
	}

	// the anthropic-go-sdk library seems to have a bug where if the stream
	// stops midway, we don't get an error back in stream.Err(). this can be
	// reproduced not passing in the httpclient, and causing a tool call
	// response where a single chunk may take some time (>3s roughly). to detect
	// this scenario, we check that all content blocks that are started are also
	// stopped.
	if startedBlocks != stoppedBlocks {
		log.Error().Int("started", startedBlocks).Int("stopped", stoppedBlocks).Msg("Anthropic tool chat: number of started and stopped content blocks do not match: did something disconnect?")
		return nil, fmt.Errorf("anthropic tool chat failure: started %d blocks but stopped %d", startedBlocks, stoppedBlocks)
	}

	log.Trace().Interface("responseMessage", finalMessage).Msg("Anthropic tool chat response message")

	response, err := anthropicToChatMessageResponse(finalMessage, options.Params.Provider)
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
	var anthropicMessages []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion

		if msg.Content != "" {
			if msg.Role == ChatMessageRoleTool {
				block := anthropic.NewToolResultBlock(msg.ToolCallId, invalidJsonWorkaround(msg.Content), msg.IsError)
				if msg.CacheControl != "" {
					block.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
						Type: anthropic.F(anthropic.CacheControlEphemeralType(msg.CacheControl)),
					})
				}
				blocks = append(blocks, block)
			} else {
				block := anthropic.NewTextBlock(invalidJsonWorkaround(msg.Content))
				if msg.CacheControl != "" {
					block.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
						Type: anthropic.F(anthropic.CacheControlEphemeralType(msg.CacheControl)),
					})
				}
				blocks = append(blocks, block)
			}
		}
		for _, toolCall := range msg.ToolCalls {
			args := make(map[string]any, 0)
			err := json.Unmarshal([]byte(RepairJson(toolCall.Arguments)), &args)
			if err != nil {
				// anthropic requires valid json, but didn't give us valid json. we improvise.
				args["invalid_json_stringified"] = toolCall.Arguments
			}
			toolUseBlock := anthropic.NewToolUseBlockParam(toolCall.Id, toolCall.Name, args)
			if msg.CacheControl != "" {
				toolUseBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
					Type: anthropic.F(anthropic.CacheControlEphemeralType(msg.CacheControl)),
				})
			}
			blocks = append(blocks, toolUseBlock)
		}
		anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
			Role:    anthropic.F(anthropicFromChatMessageRole(msg.Role)),
			Content: anthropic.F(blocks),
		})
	}

	// anthropic doesn't allow multiple consecutive messages from the same role
	var mergedAnthropicMessages []anthropic.MessageParam
	for _, msg := range anthropicMessages {
		if len(mergedAnthropicMessages) == 0 {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
			continue
		}

		lastMsg := &mergedAnthropicMessages[len(mergedAnthropicMessages)-1]
		if lastMsg.Role == msg.Role {
			lastMsg.Content.Value = append(lastMsg.Content.Value, msg.Content.Value...)
		} else {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
		}
	}

	return mergedAnthropicMessages, nil
}

func invalidJsonWorkaround(s string) string {
	// replace "\x1b" with "" to avoid invalid json error from anthropic
	// with current version of sdk
	return strings.ReplaceAll(s, "\x1b", "")
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
		result = append(result, anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F[interface{}](tool.Parameters),
		})
	}
	return result, nil
}

func anthropicToChatMessageResponse(message anthropic.Message, provider string) (*ChatMessageResponse, error) {
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
		Provider: provider,
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
