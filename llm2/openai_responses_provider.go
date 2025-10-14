package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"go.temporal.io/sdk/activity"
)

const defaultModel = "gpt-5-codex"

func supportsReasoning(model string) bool {
	return model == "gpt-5-nano" || model == "gpt-5-codex"
}

type OpenAIResponsesProvider struct{}

func (p OpenAIResponsesProvider) Stream(ctx context.Context, options Options, eventChan chan<- Event) (*MessageResponse, error) {
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
				if activity.IsActivity(ctx) {
					activity.RecordHeartbeat(ctx, map[string]bool{"fake": true})
				}
			}
		}
	}()

	providerNameNormalized := options.Params.ModelConfig.NormalizedProviderName()
	token, err := options.Secrets.SecretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, err
	}

	client := openai.NewClient(option.WithAPIKey(token))

	model := options.Params.Model
	if model == "" {
		model = defaultModel
	}

	inputItems, err := messageToResponsesInput(options.Params.Messages)
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

	if len(options.Params.Tools) > 0 {
		toolsToUse := options.Params.Tools
		if options.Params.ToolChoice.Type == common.ToolChoiceTypeTool {
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

	params.Store = openai.Bool(false)
	if supportsReasoning(model) {
		includeSet := make(map[responses.ResponseIncludable]bool)
		includeSet["reasoning.encrypted_content"] = true
		for _, inc := range params.Include {
			includeSet[inc] = true
		}
		params.Include = make([]responses.ResponseIncludable, 0, len(includeSet))
		for inc := range includeSet {
			params.Include = append(params.Include, inc)
		}
	}

	stream := client.Responses.NewStreaming(ctx, params)

	var events []Event
	callIDToIndex := make(map[string]int)
	var textBlockIndex int = -1
	var reasoningBlockIndex int = -1
	var lastToolCallIndex int = -1
	nextBlockIndex := 0

	var stopReason string
	var usage Usage

loop:
	for stream.Next() {
		data := stream.Current()

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

		case responses.ResponseOutputItemAddedEvent:
			event := data.AsResponseOutputItemAdded()
			switch event.Item.AsAny().(type) {
			case responses.ResponseFunctionToolCall:
				item := event.Item.AsFunctionCall()
				callIDToIndex[item.CallID] = nextBlockIndex
				lastToolCallIndex = nextBlockIndex
				evt := Event{
					Type:  EventBlockStarted,
					Index: nextBlockIndex,
					ContentBlock: &ContentBlock{
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        item.CallID,
							Name:      item.Name,
							Arguments: "",
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)
				nextBlockIndex++

			case responses.ResponseReasoningItem:
				item := event.Item.AsReasoning()
				if reasoningBlockIndex == -1 {
					reasoningBlockIndex = nextBlockIndex
					evt := Event{
						Type:  EventBlockStarted,
						Index: reasoningBlockIndex,
						ContentBlock: &ContentBlock{
							Type: ContentBlockTypeReasoning,
							Reasoning: &ReasoningBlock{
								Text:    "",
								Summary: "",
							},
						},
					}
					eventChan <- evt
					events = append(events, evt)
					nextBlockIndex++
				}
				if item.EncryptedContent != "" {
					evt := Event{
						Type:  EventSignatureDelta,
						Index: reasoningBlockIndex,
						Delta: item.EncryptedContent,
					}
					eventChan <- evt
					events = append(events, evt)
				}
			}

		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			event := data.AsResponseFunctionCallArgumentsDelta()
			if lastToolCallIndex >= 0 {
				evt := Event{
					Type:  EventTextDelta,
					Index: lastToolCallIndex,
					Delta: event.Delta,
				}
				eventChan <- evt
				events = append(events, evt)
			}

		case responses.ResponseTextDeltaEvent:
			if textBlockIndex == -1 {
				textBlockIndex = nextBlockIndex
				evt := Event{
					Type:  EventBlockStarted,
					Index: textBlockIndex,
					ContentBlock: &ContentBlock{
						Type: ContentBlockTypeText,
						Text: "",
					},
				}
				eventChan <- evt
				events = append(events, evt)
				nextBlockIndex++
			}
			evt := Event{
				Type:  EventTextDelta,
				Index: textBlockIndex,
				Delta: data.Delta,
			}
			eventChan <- evt
			events = append(events, evt)

		case responses.ResponseReasoningTextDeltaEvent:
			if reasoningBlockIndex == -1 {
				reasoningBlockIndex = nextBlockIndex
				evt := Event{
					Type:  EventBlockStarted,
					Index: reasoningBlockIndex,
					ContentBlock: &ContentBlock{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text:    "",
							Summary: "",
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)
				nextBlockIndex++
			}
			evt := Event{
				Type:  EventTextDelta,
				Index: reasoningBlockIndex,
				Delta: data.Delta,
			}
			eventChan <- evt
			events = append(events, evt)

		case responses.ResponseReasoningSummaryTextDeltaEvent:
			if reasoningBlockIndex >= 0 {
				evt := Event{
					Type:  EventSummaryTextDelta,
					Index: reasoningBlockIndex,
					Delta: data.Delta,
				}
				eventChan <- evt
				events = append(events, evt)
			}
			if textBlockIndex == -1 {
				textBlockIndex = nextBlockIndex
				evt := Event{
					Type:  EventBlockStarted,
					Index: textBlockIndex,
					ContentBlock: &ContentBlock{
						Type: ContentBlockTypeText,
						Text: "",
					},
				}
				eventChan <- evt
				events = append(events, evt)
				nextBlockIndex++
			}
			evt := Event{
				Type:  EventTextDelta,
				Index: textBlockIndex,
				Delta: data.Delta,
			}
			eventChan <- evt
			events = append(events, evt)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	outputMessage := applyEventsToMessage(events)

	return &MessageResponse{
		Id:           "",
		Model:        model,
		Provider:     options.Params.Provider,
		Output:       outputMessage,
		StopReason:   stopReason,
		StopSequence: "",
		Usage:        usage,
	}, nil
}

func messageToResponsesInput(messages []Message) ([]responses.ResponseInputItemUnionParam, error) {
	var items []responses.ResponseInputItemUnionParam

	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case ContentBlockTypeText:
				var role responses.EasyInputMessageRole
				switch msg.Role {
				case RoleUser:
					role = responses.EasyInputMessageRoleUser
				case RoleSystem:
					role = responses.EasyInputMessageRoleSystem
				case RoleAssistant:
					role = responses.EasyInputMessageRoleAssistant
				default:
					return nil, fmt.Errorf("unsupported role %s for text block", msg.Role)
				}
				items = append(items, responses.ResponseInputItemParamOfMessage(
					block.Text,
					role,
				))

			case ContentBlockTypeToolUse:
				if msg.Role != RoleAssistant {
					return nil, fmt.Errorf("tool_use blocks must be in assistant messages, got role %s", msg.Role)
				}
				if block.ToolUse == nil {
					return nil, fmt.Errorf("tool_use block missing ToolUse data")
				}
				if block.ToolUse.Id == "" {
					return nil, fmt.Errorf("tool_use block missing Id")
				}
				if block.ToolUse.Name == "" {
					return nil, fmt.Errorf("tool_use block missing Name")
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(
					block.ToolUse.Arguments,
					block.ToolUse.Id,
					block.ToolUse.Name,
				))

			case ContentBlockTypeToolResult:
				if block.ToolResult == nil {
					return nil, fmt.Errorf("tool_result block missing ToolResult data")
				}
				if block.ToolResult.ToolCallId == "" {
					return nil, fmt.Errorf("tool_result block missing ToolCallId")
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(
					block.ToolResult.ToolCallId,
					block.ToolResult.Text,
				))

			case ContentBlockTypeReasoning:
				if msg.Role != RoleAssistant {
					return nil, fmt.Errorf("reasoning blocks must be in assistant messages, got role %s", msg.Role)
				}
				if block.Reasoning != nil && block.Reasoning.EncryptedContent != "" {
					summary := block.Reasoning.Summary
					if summary == "" {
						summary = block.Reasoning.Text
					}
					if summary == "" {
						summary = "[reasoning]"
					}
					reasoningJSON := fmt.Sprintf(`{"type":"reasoning","encrypted_content":%q,"summary":%q}`, block.Reasoning.EncryptedContent, summary)
					reasoningItem := responses.ResponseInputItemUnionParam{}
					if err := reasoningItem.UnmarshalJSON([]byte(reasoningJSON)); err != nil {
						return nil, fmt.Errorf("failed to construct reasoning input item: %w", err)
					}
					items = append(items, reasoningItem)
				} else if block.Reasoning != nil && block.Reasoning.Text != "" {
					items = append(items, responses.ResponseInputItemParamOfMessage(
						block.Reasoning.Text,
						responses.EasyInputMessageRoleAssistant,
					))
				}

			case ContentBlockTypeRefusal:
				if msg.Role != RoleAssistant {
					return nil, fmt.Errorf("refusal blocks must be in assistant messages, got role %s", msg.Role)
				}
				text := ""
				if block.Refusal != nil {
					text = block.Refusal.Reason
				}
				items = append(items, responses.ResponseInputItemParamOfMessage(
					text,
					responses.EasyInputMessageRoleAssistant,
				))

			case ContentBlockTypeImage, ContentBlockTypeFile, ContentBlockTypeMcpCall:
				return nil, fmt.Errorf("unsupported content block type: %s", block.Type)

			default:
				return nil, fmt.Errorf("unknown content block type: %s", block.Type)
			}
		}
	}

	return items, nil
}

func applyEventsToMessage(events []Event) Message {
	blocks := make(map[int]*ContentBlock)
	maxIndex := -1

	for _, evt := range events {
		if evt.Index > maxIndex {
			maxIndex = evt.Index
		}

		switch evt.Type {
		case EventBlockStarted:
			if evt.ContentBlock != nil {
				blockCopy := *evt.ContentBlock
				if blockCopy.Type == ContentBlockTypeToolUse && blockCopy.ToolUse != nil {
					toolUseCopy := *blockCopy.ToolUse
					blockCopy.ToolUse = &toolUseCopy
				} else if blockCopy.Type == ContentBlockTypeReasoning && blockCopy.Reasoning != nil {
					reasoningCopy := *blockCopy.Reasoning
					blockCopy.Reasoning = &reasoningCopy
				}
				blocks[evt.Index] = &blockCopy
			}

		case EventTextDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Type == ContentBlockTypeText {
					block.Text += evt.Delta
				} else if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Text += evt.Delta
				} else if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
					block.ToolUse.Arguments += evt.Delta
				}
			}

		case EventSummaryTextDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Type == ContentBlockTypeText {
					block.Text += evt.Delta
				} else if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Summary += evt.Delta
				}
			}

		case EventSignatureDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Reasoning == nil {
					block.Reasoning = &ReasoningBlock{}
				}
				block.Reasoning.EncryptedContent += evt.Delta
			}
		}
	}

	orderedBlocks := make([]ContentBlock, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		if block, ok := blocks[i]; ok {
			orderedBlocks = append(orderedBlocks, *block)
		}
	}

	return Message{
		Role:    RoleAssistant,
		Content: orderedBlocks,
	}
}

func openaiResponsesFromTools(tools []*common.Tool) ([]responses.ToolUnionParam, error) {
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

func openaiResponsesFromToolChoice(toolChoice common.ToolChoice, tools []*common.Tool) *responses.ResponseNewParamsToolChoiceUnion {
	if len(tools) == 0 {
		return nil
	}

	var mode responses.ToolChoiceOptions
	switch toolChoice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		mode = responses.ToolChoiceOptionsAuto
	case common.ToolChoiceTypeRequired:
		mode = responses.ToolChoiceOptionsRequired
	case common.ToolChoiceTypeTool:
		mode = responses.ToolChoiceOptionsRequired
	default:
		panic("Unknown tool choice: " + string(toolChoice.Type))
	}

	return &responses.ResponseNewParamsToolChoiceUnion{
		OfToolChoiceMode: param.NewOpt(mode),
	}
}

func filterToolsByName(tools []*common.Tool, name string) []*common.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return []*common.Tool{tool}
		}
	}
	return tools
}
