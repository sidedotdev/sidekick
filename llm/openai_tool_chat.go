package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sidekick/utils"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const OpenaiDefaultModel = "gpt-4o-2024-05-13"
const OpenaiDefaultLongContextModel = "gpt-4o-2024-05-13"
const OpenaiApiKeySecretName = "OPENAI_API_KEY"

type OpenaiToolChat struct{}

// implements ToolChat interface
func (OpenaiToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error) {
	token, err := options.Secrets.SecretManager.GetSecret(OpenaiApiKeySecretName)
	if err != nil {
		return nil, err
	}

	client := openai.NewClient(token)

	var temperature float32 = defaultTemperature
	if options.Params.Temperature != nil {
		temperature = *options.Params.Temperature
	}

	var model string = OpenaiDefaultModel
	if options.Params.Model != "" {
		model = options.Params.Model
	}

	var parallelToolCalls bool = false
	if options.Params.ParallelToolCalls != nil {
		parallelToolCalls = *options.Params.ParallelToolCalls
	}

	req := openai.ChatCompletionRequest{
		Model:             model,
		Messages:          openaiFromChatMessages(options.Params.Messages),
		ToolChoice:        openaiFromToolChoice(options.Params.ToolChoice, options.Params.Tools),
		Tools:             openaiFromTools(options.Params.Tools),
		Stream:            true,
		Temperature:       temperature,
		ParallelToolCalls: parallelToolCalls,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if len(req.Tools) == 0 {
		req.ParallelToolCalls = nil
	}
	stream, err := client.CreateChatCompletionStream(ctx, req)

	if err != nil {
		return nil, err
	}

	defer stream.Close()
	defer close(deltaChan)

	var deltas []ChatMessageDelta
	var finishReason openai.FinishReason
	var usage *openai.Usage
	for {
		res, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if len(res.Choices) == 0 {
			// TODO /gen create a ChatMessageDelta here based on res.Usage, and
			// send it to the deltaChan
			usage = res.Usage
			break
		}

		choice := res.Choices[0]

		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		delta := cleanupDelta(openaiToChatMessageDelta(res, choice))
		deltaChan <- delta
		deltas = append(deltas, delta)
	}

	return &ChatMessageResponse{
		ChatMessage: *stitchDeltasToMessage(deltas),
		StopReason:  string(finishReason), // TODO /gen convert this properly, once we have an enum defined for stop reasons
		Usage:       openaiToUsage(usage),
		Model:       model,
		Provider:    OpenaiChatProvider,
	}, nil
}

func openaiFromChatMessages(messages []ChatMessage) []openai.ChatCompletionMessage {
	return utils.Map(messages, func(msg ChatMessage) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCalls:  openaiFromToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallId,
			Name:       msg.Name,
		}
	})
}

func openaiFromTools(tools []*Tool) []openai.Tool {
	return utils.Map(tools, func(tool *Tool) openai.Tool {
		return openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	})
}

func openaiFromToolChoice(toolChoice ToolChoice, tools []*Tool) any {
	if len(tools) == 0 {
		return nil
	}
	switch toolChoice.Type {
	case ToolChoiceTypeAuto, ToolChoiceTypeUnspecified:
		return "auto"
	case ToolChoiceTypeRequired:
		return "required"
	case ToolChoiceTypeTool:
		return openai.ToolChoice{
			Type: openai.ToolTypeFunction,
			Function: openai.ToolFunction{
				Name: toolChoice.Name,
			},
		}
	default:
		panic(fmt.Sprintf("unsupported tool choice type: %v", toolChoice.Type))
	}
}

func openaiToChatMessageDelta(res openai.ChatCompletionStreamResponse, choice openai.ChatCompletionStreamChoice) ChatMessageDelta {
	return ChatMessageDelta{
		Role:      ChatMessageRole(choice.Delta.Role),
		Content:   choice.Delta.Content,
		ToolCalls: openaiToToolCalls(choice.Delta.ToolCalls),
		Usage:     openaiToUsage(res.Usage),
	}
}

func openaiFromToolCalls(toolCalls []ToolCall) []openai.ToolCall {
	return utils.Map(toolCalls, func(tc ToolCall) openai.ToolCall {
		return openai.ToolCall{
			Type: openai.ToolTypeFunction,
			ID:   tc.Id,
			Function: openai.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		}
	})
}

func openaiToToolCalls(toolCalls []openai.ToolCall) []ToolCall {
	return utils.Map(toolCalls, func(tc openai.ToolCall) ToolCall {
		return ToolCall{
			Id:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		}
	})
}

func openaiToUsage(usage *openai.Usage) Usage {
	if usage == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
	}
}

func cleanupDelta(delta ChatMessageDelta) ChatMessageDelta {
	if delta.ToolCalls != nil {
		for i, toolCall := range delta.ToolCalls {
			if toolCall.Name != "" {
				// cleanup rarely-occurring bad syntax from openai
				toolCall.Name = strings.TrimPrefix(toolCall.Name, "tools.")
				toolCall.Name = strings.TrimPrefix(toolCall.Name, "tool.")
				toolCall.Name = strings.TrimPrefix(toolCall.Name, "functions.")
				toolCall.Name = strings.TrimPrefix(toolCall.Name, "function.")
			}
			delta.ToolCalls[i] = toolCall
		}
	}
	return delta
}

func stitchDeltasToMessage(deltas []ChatMessageDelta) *ChatMessage {
	var contentBuilder strings.Builder
	var nameBuilder strings.Builder
	var argsBuilder strings.Builder
	var toolCalls []ToolCall
	var role ChatMessageRole

	currentToolCall := &ToolCall{}
	for _, delta := range deltas {
		if delta.Content != "" {
			contentBuilder.WriteString(delta.Content)
		}

		if delta.Role != "" {
			role = delta.Role
		}

		if delta.ToolCalls != nil {
			for _, toolCallDelta := range delta.ToolCalls {
				// Since the function call content is also being received in chunks,
				// we need to build it in parts just like the content.

				// infer if it is a new tool/function call
				if toolCallDelta.Name != "" || toolCallDelta.Id != "" {
					// confirm we have all the details already (or none)
					// if we have all, it's a new call, otherwise it isn't
					lastCallComplete := currentToolCall.Id != "" && currentToolCall.Name != ""
					if lastCallComplete {
						currentToolCall.Name = nameBuilder.String()
						currentToolCall.Arguments = argsBuilder.String()
						toolCalls = append(toolCalls, *currentToolCall)
						currentToolCall = &ToolCall{}
					}
				}

				if toolCallDelta.Id != "" {
					currentToolCall.Id = toolCallDelta.Id
				}

				if toolCallDelta.Arguments != "" {
					argsBuilder.WriteString(toolCallDelta.Arguments)
				}

				if toolCallDelta.Name != "" {
					nameBuilder.WriteString(toolCallDelta.Name)
				}
			}
		}
	}

	// Assign the last tool call to the message
	if currentToolCall.Id != "" {
		currentToolCall.Name = nameBuilder.String()
		currentToolCall.Arguments = argsBuilder.String()
		toolCalls = append(toolCalls, *currentToolCall)
		currentToolCall = &ToolCall{}
	}

	return &ChatMessage{
		Role:      role,
		Content:   contentBuilder.String(),
		ToolCalls: toolCalls,
	}
}
