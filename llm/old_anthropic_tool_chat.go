package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sidekick/utils"

	old_anthropic "github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic/client/native"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

type OldAnthropicToolChat struct{}

func (OldAnthropicToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error) {
	token, err := options.Secrets.SecretManager.GetSecret(AnthropicApiKeySecretName)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 180 * time.Second}
	client, err := native.MakeClient(native.Config{
		APIKey:     token,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, err
	}

	var model string = AnthropicDefaultModel
	if options.Params.Model != "" {
		model = options.Params.Model
	}

	var temperature float32 = defaultTemperature
	if options.Params.Temperature != nil {
		temperature = *options.Params.Temperature
	}

	request := &old_anthropic.MessageRequest{
		Model:             old_anthropic.Model(model),
		Temperature:       float64(temperature),
		MaxTokensToSample: 4000,
		Messages:          oldAnthropicFromChatMessages(options.Params.Messages),
		ToolChoice:        oldAnthropicFromToolChoice(options.Params.ToolChoice, options.Params.Tools),
		Tools:             oldAnthropicFromTools(options.Params.Tools),
		Stream:            true,
	}
	//utils.PrettyPrint(request)

	rCh, errCh := client.MessageStream(ctx, request)

	chunk := &old_anthropic.MessageStreamResponse{}
	done := false
	messageResponse := old_anthropic.MessageResponse{}
	var currentContentBlock old_anthropic.ContentBlock
	currentBuilder := strings.Builder{}

	for {
		select {
		case chunk = <-rCh:
			// Record heartbeat only if we're in an activity context to avoid panic.
			// TODO also do this in the activity wrapper
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, chunk)
			}

			switch chunk.Type {
			case string(old_anthropic.MessageEventTypePing):
				continue
			case string(old_anthropic.MessageEventTypeMessageStart):
				messageResponse = chunk.Message
			case string(old_anthropic.MessageEventTypeMessageDelta):
				// TODO handle MessageEventTypeMessageDelta properly
				//deltaChan <- anthropicMessageDeltaToChatMessageDelta(chunk.ContentBlock)
				//if chunk.Delta.StopReason != "" {
				//	messageResponse.StopReason = chunk.Delta.StopReason
				//}
				//if chunk.Delta.StopSequence != "" {
				//	messageResponse.StopSequence = chunk.Delta.StopSequence
				//}
				//if chunk.Usage.InputTokens != 0 {
				//	messageResponse.Usage.InputTokens = chunk.Usage.InputTokens
				//}
				//if chunk.Usage.OutputTokens != 0 {
				//	messageResponse.Usage.OutputTokens = chunk.Usage.OutputTokens
				//}
				break
			case string(old_anthropic.MessageEventTypeMessageStop):
				done = true
			case string(old_anthropic.MessageEventTypeContentBlockStart):
				deltaChan <- oldAnthropicContentStartToChatMessageDelta(chunk.ContentBlock)
				currentContentBlock = chunk.ContentBlock
			case string(old_anthropic.MessageEventTypeContentBlockStop):
				partResponse := toPartResponse(currentContentBlock, currentBuilder.String())
				messageResponse.Content = append(messageResponse.Content, partResponse)
				currentContentBlock = nil
				currentBuilder.Reset()
			case string(old_anthropic.MessageEventTypeContentBlockDelta):
				deltaChan <- oldAnthropicToChatMessageDelta(chunk.Delta)
				switch chunk.Delta.Type {
				case "text_delta":
					currentBuilder.WriteString(chunk.Delta.Text)
				case "input_json_delta":
					currentBuilder.WriteString(chunk.Delta.PartialJson)
				}
			default:
				panic("unexpected event type: " + chunk.Type)
			}
		case err := <-errCh:
			log.Error().Err(err).Msg("Error in anthropic ChatStream")
			return nil, err
		}

		if chunk.Type == "message_stop" || done {
			break
		}
	}

	close(deltaChan)
	return oldAnthropicToChatMessageResponse(messageResponse), nil
}

func toPartResponse(contentBlock old_anthropic.ContentBlock, content string) old_anthropic.MessagePartResponse {
	switch contentBlock.ContentBlockType() {
	case "text":
		return old_anthropic.MessagePartResponse{
			Type: contentBlock.ContentBlockType(),
			Text: content,
		}
	case "tool_use":
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(content), &input); err != nil {
			panic(err)
		}

		toolUseContentBlock := contentBlock.(old_anthropic.ToolUseContentBlock)
		return old_anthropic.MessagePartResponse{
			Type:  toolUseContentBlock.Type,
			ID:    toolUseContentBlock.ID,
			Name:  toolUseContentBlock.Name,
			Input: input,
		}
	default:
		panic("unexpected content block type: " + contentBlock.ContentBlockType())
	}
}

func oldAnthropicToChatMessageResponse(response old_anthropic.MessageResponse) *ChatMessageResponse {
	chatMessage := &ChatMessageResponse{
		ChatMessage: ChatMessage{
			Role: ChatMessageRoleAssistant,
		},
		StopReason:   response.StopReason,
		StopSequence: response.StopSequence,
		Usage: Usage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		},
		Model:    string(response.Model),
		Provider: "anthropic",
	}

	for _, part := range response.Content {
		if part.Type == "text" {
			chatMessage.Content += part.Text
		} else if part.Type == "tool_use" {
			arguments := utils.PanicJSON(part.Input)
			chatMessage.ToolCalls = append(chatMessage.ToolCalls, ToolCall{
				Id:        part.ID,
				Name:      part.Name,
				Arguments: arguments,
			})
		}
	}

	return chatMessage
}

func oldAnthropicFromChatMessages(messages []ChatMessage) []old_anthropic.MessagePartRequest {
	anthropicMessages := utils.Map(messages, func(msg ChatMessage) old_anthropic.MessagePartRequest {
		anthropicMessage := old_anthropic.MessagePartRequest{
			Role: oldAnthropicFromChatMessageRole(msg.Role),
		}
		// only add content if it's not empty: anthropic doesn't allow empty content blocks
		if msg.Content != "" {
			// the tool role is not used by anthropic, but rather tool_result content blocks
			if msg.Role == ChatMessageRoleTool {
				anthropicMessage.Content = append(anthropicMessage.Content, old_anthropic.NewToolResultContentBlock(msg.ToolCallId, msg.Content, msg.IsError))
			} else {
				anthropicMessage.Content = append(anthropicMessage.Content, old_anthropic.NewTextContentBlock(msg.Content))
			}
		}
		if msg.ToolCalls != nil {
			for _, toolCall := range msg.ToolCalls {
				anthropicMessage.Content = append(anthropicMessage.Content, old_anthropic.ToolUseContentBlock{
					Type:  "tool_use",
					ID:    toolCall.Id,
					Name:  toolCall.Name,
					Input: utils.PanicParseMapJSON(RepairJson(toolCall.Arguments)),
				})
			}
		}
		if len(anthropicMessage.Content) == 0 {
			log.Error().Msgf("Empty anthropic content list: %s\n", utils.PrettyJSON(msg))
		}
		return anthropicMessage
	})

	// not sure why we have empty messages, but we must filter them out to avoid anthropic errors
	anthropicMessages = utils.Filter(anthropicMessages, func(msg old_anthropic.MessagePartRequest) bool {
		return len(msg.Content) > 0
	})

	// anthropic doesn't allow multiple consecutive messages from the same role
	var mergedAnthropicMessages []old_anthropic.MessagePartRequest
	for _, msg := range anthropicMessages {
		if len(mergedAnthropicMessages) == 0 {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
			continue
		}

		lastMsg := &mergedAnthropicMessages[len(mergedAnthropicMessages)-1]
		if lastMsg.Role == msg.Role {
			lastMsg.Content = append(lastMsg.Content, msg.Content...)
		} else {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
		}
	}

	return mergedAnthropicMessages
}

func oldAnthropicFromToolChoice(toolChoice ToolChoice, tools []*Tool) *old_anthropic.ToolChoice {
	if len(tools) == 0 {
		return nil
	}

	switch toolChoice.Type {
	case ToolChoiceTypeAuto:
		return &old_anthropic.ToolChoice{
			Type: "auto",
		}
	case ToolChoiceTypeRequired:
		return &old_anthropic.ToolChoice{
			Type: "any",
		}
	case ToolChoiceTypeTool:
		return &old_anthropic.ToolChoice{
			Type: "tool",
			Name: toolChoice.Name,
		}
	case ToolChoiceTypeUnspecified:
		return &old_anthropic.ToolChoice{
			Type: "auto",
		}
	}
	panic(fmt.Sprintf("unsupported tool choice type: %v", toolChoice.Type))
}

func oldAnthropicFromChatMessageRole(role ChatMessageRole) string {
	switch role {
	case ChatMessageRoleUser, ChatMessageRoleSystem, ChatMessageRoleTool:
		return "user"
	case ChatMessageRoleAssistant:
		return "assistant"
	}
	panic(fmt.Sprintf("unsupported role: %v", role))
}

func oldAnthropicFromTools(tools []*Tool) []old_anthropic.Tool {
	return utils.Map(tools, func(tool *Tool) old_anthropic.Tool {
		return old_anthropic.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		}
	})
}

func oldAnthropicToChatMessageDelta(delta old_anthropic.MessageStreamDelta) ChatMessageDelta {
	outDelta := ChatMessageDelta{Role: ChatMessageRoleAssistant}
	switch delta.Type {
	case "text_delta":
		outDelta.Content = delta.Text
	case "input_json_delta":
		outDelta.ToolCalls = append(outDelta.ToolCalls, ToolCall{
			Arguments: delta.PartialJson,
		})
	default:
		panic(fmt.Sprintf("unsupported delta type: %v", delta.Type))
	}
	return outDelta
}

func oldAnthropicContentStartToChatMessageDelta(contentBlock old_anthropic.ContentBlock) ChatMessageDelta {
	switch contentBlock.ContentBlockType() {
	case "text":
		return ChatMessageDelta{
			Role:    ChatMessageRoleAssistant,
			Content: contentBlock.(old_anthropic.TextContentBlock).Text,
		}
	case "tool_use":
		toolUseContentBlock := contentBlock.(old_anthropic.ToolUseContentBlock)
		return ChatMessageDelta{
			Role: ChatMessageRoleAssistant,
			ToolCalls: []ToolCall{
				{
					Id:   toolUseContentBlock.ID,
					Name: toolUseContentBlock.Name,
				},
			},
		}
	default:
		panic(fmt.Sprintf("unsupported content block type: %v", contentBlock.ContentBlockType()))
	}
}
