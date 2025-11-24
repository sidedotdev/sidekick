package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sidekick/common"
	"sidekick/utils"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

const OpenaiResponsesDefaultModel = "gpt-5-codex"

type OpenaiResponsesToolChat struct {
	BaseURL      string
	DefaultModel string
}

func (o OpenaiResponsesToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta, progressChan chan<- ProgressInfo) (*ChatMessageResponse, error) {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
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
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	clientOptions := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithMaxRetries(0), // retries handled by temporal
		option.WithHTTPClient(httpClient),
	}

	if o.BaseURL != "" {
		clientOptions = append(clientOptions, option.WithBaseURL(o.BaseURL))
	}

	client := openai.NewClient(clientOptions...)

	var model string
	if options.Params.Model != "" {
		model = options.Params.Model
	} else if o.DefaultModel != "" {
		model = o.DefaultModel
	} else {
		model = OpenaiResponsesDefaultModel
	}

	inputItems, err := buildStructuredInputFromMessages(options.Params.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to build input: %w", err)
	}

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		},
		Model: openai.ChatModel(model),
	}

	if options.Params.Temperature != nil {
		params.Temperature = openai.Float(float64(*options.Params.Temperature))
	}

	var parallelToolCalls bool = false
	if options.Params.ParallelToolCalls != nil {
		parallelToolCalls = *options.Params.ParallelToolCalls
	}
	params.ParallelToolCalls = param.NewOpt(parallelToolCalls)

	params.Store = openai.Bool(false)

	var actualReasoningEffort string
	modelInfo, _ := common.GetModel(options.Params.Provider, model)
	if modelInfo != nil && modelInfo.Reasoning {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
		params.Reasoning.Summary = shared.ReasoningSummaryAuto
		if options.Params.ReasoningEffort != "" {
			actualReasoningEffort = options.Params.ReasoningEffort
			params.Reasoning.Effort = shared.ReasoningEffort(actualReasoningEffort)
		}
	}

	if len(options.Params.Tools) > 0 {
		toolsToUse := options.Params.Tools
		if options.Params.ToolChoice.Type == ToolChoiceTypeTool {
			toolsToUse = filterToolsByName(options.Params.Tools, options.Params.ToolChoice.Name)
		}

		tools, err := openaiResponsesFromTools(toolsToUse)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
		params.Tools = tools

		toolChoice := openaiResponsesFromToolChoice(options.Params.ToolChoice, toolsToUse)
		if toolChoice != nil {
			params.ToolChoice = *toolChoice
		}
	}

	stream := client.Responses.NewStreaming(ctx, params)

	var deltas []ChatMessageDelta
	var stopReason string
	var usage Usage

loop:
	for stream.Next() {
		data := stream.Current()
		log.Trace().Msgf("OPENAI CHUNK: %s\n", utils.PanicJSON(data))

		switch data.AsAny().(type) {
		case responses.ResponseCompletedEvent:
			response := data.Response
			if response.IncompleteDetails.Reason != "" {
				stopReason = string(response.IncompleteDetails.Reason)
			} else {
				switch response.Status {
				case responses.ResponseStatusCompleted:
					stopReason = "stop"
				case responses.ResponseStatusFailed:
					stopReason = "failed"
				case responses.ResponseStatusCancelled:
					stopReason = "cancelled"
				default:
					stopReason = fmt.Sprintf("response_status=%s", response.Status)
				}
			}
			if response.Usage.InputTokens > 0 {
				usage.InputTokens = int(response.Usage.InputTokens)
			}
			if response.Usage.OutputTokens > 0 {
				usage.OutputTokens = int(response.Usage.OutputTokens)
			}
			break loop
		case responses.ResponseReasoningSummaryTextDeltaEvent:
			continue

		case responses.ResponseOutputItemAddedEvent:
			event := data.AsResponseOutputItemAdded()
			switch event.Item.AsAny().(type) {
			case responses.ResponseFunctionToolCall:
				item := event.Item.AsFunctionCall()
				delta := ChatMessageDelta{
					Role: ChatMessageRoleAssistant,
					ToolCalls: []ToolCall{
						{Id: item.CallID, Name: item.Name, Arguments: item.Arguments},
					},
				}
				delta = cleanupDelta(delta)
				deltaChan <- delta
				deltas = append(deltas, delta)
			}
		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			event := data.AsResponseFunctionCallArgumentsDelta()
			delta := ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
				ToolCalls: []ToolCall{
					{Arguments: event.Delta},
				},
			}
			delta = cleanupDelta(delta)
			deltaChan <- delta
			deltas = append(deltas, delta)
		case responses.ResponseReasoningTextDeltaEvent, responses.ResponseTextDeltaEvent:
			delta := ChatMessageDelta{
				Role:    ChatMessageRoleAssistant,
				Content: data.Delta,
			}
			delta = cleanupDelta(delta)
			deltaChan <- delta
			deltas = append(deltas, delta)
		default:
			log.Trace().Msgf("Unhandled event: %s\n", data.Type)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	message := stitchDeltasToMessage(deltas, false)
	if message.Role == "" && len(deltas) == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		err := errors.New("chat message role not found")
		log.Error().Err(err).Interface("deltas", deltas)
		return nil, err
	}

	return &ChatMessageResponse{
		ChatMessage:     message,
		StopReason:      stopReason,
		Usage:           usage,
		Model:           model,
		Provider:        options.Params.Provider,
		ReasoningEffort: actualReasoningEffort,
	}, nil
}

func buildStructuredInputFromMessages(messages []ChatMessage) ([]responses.ResponseInputItemUnionParam, error) {
	var items []responses.ResponseInputItemUnionParam

	for _, msg := range messages {
		switch msg.Role {
		case ChatMessageRoleUser:
			items = append(items, responses.ResponseInputItemParamOfMessage(
				msg.Content,
				responses.EasyInputMessageRoleUser,
			))
		case ChatMessageRoleSystem:
			items = append(items, responses.ResponseInputItemParamOfMessage(
				msg.Content,
				responses.EasyInputMessageRoleSystem,
			))
		case ChatMessageRoleAssistant:
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					items = append(items, responses.ResponseInputItemParamOfFunctionCall(
						tc.Arguments,
						tc.Id,
						tc.Name,
					))
				}
			} else if msg.Content != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					msg.Content,
					responses.EasyInputMessageRoleAssistant,
				))
			}
		case ChatMessageRoleTool:
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(
				msg.ToolCallId,
				msg.Content,
			))
		default:
			log.Error().Any("message", msg).Msgf("unsupported message role in message: %s", utils.PanicJSON(msg))
			return nil, fmt.Errorf("unsupported message role: %s", utils.PanicJSON(msg.Role))
		}
	}

	return items, nil
}

func openaiResponsesFromTools(tools []*Tool) ([]responses.ToolUnionParam, error) {
	result := make([]responses.ToolUnionParam, 0, len(tools))

	for _, tool := range tools {
		params, err := jsonSchemaToMap(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameters for tool %s: %w", tool.Name, err)
		}

		functionTool := responses.FunctionToolParam{
			Name:        tool.Name,
			Description: param.NewOpt(tool.Description),
			Parameters:  params,
		}

		result = append(result, responses.ToolUnionParam{
			OfFunction: &functionTool,
		})
	}

	return result, nil
}

func jsonSchemaToMap(schema interface{}) (map[string]any, error) {
	if schema == nil {
		return map[string]any{}, nil
	}

	jsonBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func openaiResponsesFromToolChoice(toolChoice ToolChoice, tools []*Tool) *responses.ResponseNewParamsToolChoiceUnion {
	if len(tools) == 0 {
		return nil
	}

	var mode responses.ToolChoiceOptions
	switch toolChoice.Type {
	case ToolChoiceTypeAuto, ToolChoiceTypeUnspecified:
		mode = responses.ToolChoiceOptionsAuto
	case ToolChoiceTypeRequired:
		mode = responses.ToolChoiceOptionsRequired
	case ToolChoiceTypeTool:
		mode = responses.ToolChoiceOptionsRequired
	default:
		panic("Unknown tool choice: " + string(toolChoice.Type))
	}

	return &responses.ResponseNewParamsToolChoiceUnion{
		OfToolChoiceMode: param.NewOpt(mode),
	}
}

func filterToolsByName(tools []*Tool, name string) []*Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return []*Tool{tool}
		}
	}
	return tools
}
