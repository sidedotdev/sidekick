package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	openai "github.com/sashabaranov/go-openai"
	"go.temporal.io/sdk/activity"
)

const OpenaiDefaultModel = "gpt-5-2025-08-07"
const OpenaiApiKeySecretName = "OPENAI_API_KEY"

type OpenaiToolChat struct {
	BaseURL      string
	DefaultModel string
}

// implements ToolChat interface
func (o OpenaiToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta, progressChan chan<- ProgressInfo) (*ChatMessageResponse, error) {
	// additional heartbeat until ChatStream ends: we can't rely on the delta
	// heartbeat because thinking tokens don't stream in the chat completion API,
	// resulting in a very long time between deltas and thus heartbeat timeouts.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
			case <-ctx.Done():
				return
			case <-ticker.C:
				{
					if activity.IsActivity(ctx) {
						activity.RecordHeartbeat(ctx, map[string]bool{"fake": true})
					}
					continue
				}
			}
		}
	}()

	providerNameNormalized := options.Params.ModelConfig.NormalizedProviderName()
	token, err := options.Secrets.SecretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, err
	}

	config := openai.DefaultConfig(token)
	if o.BaseURL != "" {
		config.BaseURL = o.BaseURL
	}
	client := openai.NewClientWithConfig(config)

	// some openai models don't support any temperature other than 1. to get
	// whatever the model default is, we set it to 0 here, which means it's
	// omitted in the json request sent to openai when using
	// "github.com/sashabaranov/go-openai"
	var temperature float32 = 0.0
	if options.Params.Temperature != nil {
		temperature = *options.Params.Temperature
	}

	var model string
	if options.Params.Model != "" {
		model = options.Params.Model
	} else if o.DefaultModel != "" {
		model = o.DefaultModel
	} else {
		model = OpenaiDefaultModel
	}

	var parallelToolCalls bool = false
	if options.Params.ParallelToolCalls != nil {
		parallelToolCalls = *options.Params.ParallelToolCalls
	}

	// openai-compatible endpoints may require alternating user vs assistant
	// messages, so we merge consecutive messages of the same/equivalent role.
	// this is a hacky way to infer that we should merge messages
	shouldMerge := o.BaseURL != "" && !strings.HasPrefix(model, "gpt") && !strings.HasPrefix(model, "o1-") && !strings.HasPrefix(model, "o3-")

	req := openai.ChatCompletionRequest{
		Model:             model,
		Messages:          openaiFromChatMessages(options.Params.Messages, shouldMerge),
		ToolChoice:        openaiFromToolChoice(options.Params.ToolChoice, options.Params.Tools),
		Tools:             openaiFromTools(options.Params.Tools),
		Stream:            true,
		Temperature:       temperature,
		ParallelToolCalls: parallelToolCalls,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if options.Params.ReasoningEffort != "" {
		req.ReasoningEffort = options.Params.ModelConfig.ReasoningEffort
	}
	if len(req.Tools) == 0 {
		req.ParallelToolCalls = nil
	}
	stream, err := client.CreateChatCompletionStream(ctx, req)

	if err != nil {
		return nil, err
	}

	defer stream.Close()

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
	message := stitchDeltasToMessage(deltas, false)
	if message.Role == "" {
		err := errors.New("chat message role not found")
		log.Error().Err(err).Interface("deltas", deltas)
		return nil, err
	}
	return &ChatMessageResponse{
		ChatMessage: message,
		StopReason:  string(finishReason), // TODO /gen convert this properly, once we have an enum defined for stop reasons
		Usage:       openaiToUsage(usage),
		Model:       model,
		Provider:    options.Params.Provider,
	}, nil
}

func openaiFromChatMessages(messages []ChatMessage, shouldMerge bool) []openai.ChatCompletionMessage {
	openaiMessages := utils.Map(messages, func(msg ChatMessage) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCalls:  openaiFromToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallId,
			Name:       msg.Name,
		}
	})

	if !shouldMerge {
		return openaiMessages
	}

	// openai-compatible endpoints may require alternating user vs assistant
	// messages, so we merge consecutive messages of the same/equivalent role
	var mergedMessages []openai.ChatCompletionMessage
	for _, msg := range openaiMessages {
		if len(mergedMessages) == 0 {
			mergedMessages = append(mergedMessages, msg)
			continue
		}
		lastMsg := &mergedMessages[len(mergedMessages)-1]

		// Consider tool, user, and system roles as equivalent
		isEquivalentRole := lastMsg.Role == string(msg.Role) ||
			(isUserLikeRole(lastMsg.Role) && isUserLikeRole(string(msg.Role)))

		if isEquivalentRole {
			lastMsg.Content += "\n\n" + string(msg.Role) + ":" + msg.Content
		} else {
			mergedMessages = append(mergedMessages, msg)
		}
	}

	return mergedMessages
}

func isUserLikeRole(s string) bool {
	return s == "user" || s == "system" || s == "tool"
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

func stitchDeltasToMessage(deltas []ChatMessageDelta, idRequired bool) ChatMessage {
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
				if toolCallDelta.Name != "" || (idRequired && toolCallDelta.Id != "") {
					// confirm we have all the details already (or none)
					// if we have all, it's a new call, otherwise it isn't
					lastCallComplete := (!idRequired || currentToolCall.Id != "") && nameBuilder.String() != ""
					if lastCallComplete {
						currentToolCall.Name = nameBuilder.String()
						currentToolCall.Arguments = argsBuilder.String()
						toolCalls = append(toolCalls, *currentToolCall)
						currentToolCall = &ToolCall{}
						nameBuilder.Reset()
						argsBuilder.Reset()
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
	if nameBuilder.String() != "" {
		currentToolCall.Name = nameBuilder.String()
		currentToolCall.Arguments = argsBuilder.String()
		toolCalls = append(toolCalls, *currentToolCall)
	}

	return ChatMessage{
		Role:      role,
		Content:   contentBuilder.String(),
		ToolCalls: toolCalls,
	}
}
