package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/utils"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.temporal.io/sdk/activity"
)

const defaultModel = "gpt-5-codex"

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
	modelInfo, _ := common.GetModel(options.Params.Provider, model)
	if modelInfo != nil && modelInfo.Reasoning {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
		if options.Params.ReasoningEffort != "" {
			params.Reasoning.Effort = shared.ReasoningEffort(options.Params.ReasoningEffort)
			params.Reasoning.Summary = shared.ReasoningSummaryAuto
		}
	}

	stream := client.Responses.NewStreaming(ctx, params)

	var events []Event
	var stopReason string
	var usage Usage
	reasoningItemIndexByID := make(map[string]int)

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

			for _, output := range response.Output {
				switch output.AsAny().(type) {
				case responses.ResponseReasoningItem:
					item := output.AsReasoning()
					if idx, ok := reasoningItemIndexByID[item.ID]; ok {
						evt := Event{
							Type:  EventBlockDone,
							Index: idx,
							ContentBlock: &ContentBlock{
								Type: ContentBlockTypeReasoning,
								Reasoning: &ReasoningBlock{
									Text:             reasoningTextFromOpenaiContent(item.Content),
									Summary:          reasoningSummaryFromOpenaiContent(item.Summary),
									EncryptedContent: item.EncryptedContent,
								},
							},
						}
						eventChan <- evt
						events = append(events, evt)
					}
				}
			}

			break loop

		case responses.ResponseContentPartAddedEvent:
			openaiEvent := data.AsResponseContentPartAdded()

			switch openaiEvent.Part.AsAny().(type) {
			case responses.ResponseOutputText:
				part := openaiEvent.Part.AsOutputText()
				evt := Event{
					Type:  EventBlockStarted,
					Index: int(openaiEvent.OutputIndex),
					ContentBlock: &ContentBlock{
						Id:   openaiEvent.ItemID,
						Type: ContentBlockTypeText,
						Text: part.Text,
					},
				}
				eventChan <- evt
				events = append(events, evt)
			case responses.ResponseOutputRefusal:
				part := openaiEvent.Part.AsRefusal()
				evt := Event{
					Type:  EventBlockStarted,
					Index: int(openaiEvent.OutputIndex),
					ContentBlock: &ContentBlock{
						Id:   openaiEvent.ItemID,
						Type: ContentBlockTypeRefusal,
						Refusal: &RefusalBlock{
							Reason: part.Refusal,
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)
			case responses.ResponseContentPartAddedEventPartReasoningText:
				part := openaiEvent.Part.AsReasoningText()
				evt := Event{
					Type:  EventBlockStarted,
					Index: int(openaiEvent.OutputIndex),
					ContentBlock: &ContentBlock{
						Id:   openaiEvent.ItemID,
						Type: ContentBlockTypeReasoning,
						Text: part.Text,
					},
				}
				eventChan <- evt
				events = append(events, evt)
			}
		case responses.ResponseOutputItemAddedEvent:
			openaiEvent := data.AsResponseOutputItemAdded()
			switch openaiEvent.Item.AsAny().(type) {
			// NOTE here are the other item types we might handle in the future,
			// leaving them here for reference:
			//
			//	case responses.ResponseFileSearchToolCall:
			//	case responses.ResponseFunctionWebSearch:
			//	case responses.ResponseComputerToolCall:
			//	case responses.ResponseOutputItemImageGenerationCall:
			//	case responses.ResponseCodeInterpreterToolCall:
			//	case responses.ResponseOutputItemLocalShellCall:
			//	case responses.ResponseOutputItemMcpCall:
			//	case responses.ResponseOutputItemMcpListTools:
			//	case responses.ResponseOutputItemMcpApprovalRequest:
			//	case responses.ResponseCustomToolCall:
			case responses.ResponseOutputMessage:
				// NOTE we don't have a type for the message yet as we don't
				// know if it's an output_text or a refusal, so we'll wait for
				// "response.content_part.added" before emitting the block
				// started event
				continue

			case responses.ResponseFunctionToolCall:
				item := openaiEvent.Item.AsFunctionCall()
				evt := Event{
					Type:  EventBlockStarted,
					Index: int(openaiEvent.OutputIndex),
					ContentBlock: &ContentBlock{
						Id:   item.ID,
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        item.CallID,
							Name:      item.Name,
							Arguments: item.Arguments,
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)

			case responses.ResponseReasoningItem:
				item := openaiEvent.Item.AsReasoning()
				blockIndex := int(openaiEvent.OutputIndex)
				reasoningItemIndexByID[item.ID] = blockIndex

				evt := Event{
					Type:  EventBlockStarted,
					Index: blockIndex,
					ContentBlock: &ContentBlock{
						Id:   item.ID,
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text:    reasoningTextFromOpenaiContent(item.Content),
							Summary: reasoningSummaryFromOpenaiContent(item.Summary),
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)
			}

		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			openaiEvent := data.AsResponseFunctionCallArgumentsDelta()
			evt := Event{
				Type:  EventTextDelta,
				Index: int(openaiEvent.OutputIndex),
				Delta: openaiEvent.Delta,
			}
			eventChan <- evt
			events = append(events, evt)

		case responses.ResponseTextDeltaEvent:
			openaiEvent := data.AsResponseOutputTextDelta()
			evt := Event{
				Type:  EventTextDelta,
				Index: int(openaiEvent.OutputIndex),
				Delta: openaiEvent.Delta,
			}
			eventChan <- evt
			events = append(events, evt)

		case responses.ResponseReasoningTextDeltaEvent:
			openaiEvent := data.AsResponseReasoningTextDelta()
			evt := Event{
				Type:  EventTextDelta,
				Index: int(openaiEvent.OutputIndex),
				Delta: openaiEvent.Delta,
			}
			eventChan <- evt
			events = append(events, evt)

		case responses.ResponseReasoningSummaryTextDeltaEvent:
			openaiEvent := data.AsResponseReasoningSummaryTextDelta()
			evt := Event{
				Type:  EventSummaryTextDelta,
				Index: int(openaiEvent.OutputIndex),
				Delta: openaiEvent.Delta,
			}
			eventChan <- evt
			events = append(events, evt)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	outputMessage := accumulateOpenaiEventsToMessage(events)

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

func reasoningSummaryFromOpenaiContent(responseReasoningItemSummary []responses.ResponseReasoningItemSummary) string {
	var summary string
	for _, summaryItem := range responseReasoningItemSummary {
		summary += summaryItem.Text
	}
	return summary
}

func reasoningTextFromOpenaiContent(responseReasoningItemContent []responses.ResponseReasoningItemContent) string {
	var text string
	for _, content := range responseReasoningItemContent {
		text += content.Text
	}
	return text
}

func messageToResponsesInput(messages []Message) ([]responses.ResponseInputItemUnionParam, error) {
	var items []responses.ResponseInputItemUnionParam

	for _, msg := range messages {
	contentBlocksLoop:
		for _, block := range msg.Content {
			switch block.Type {
			case ContentBlockTypeText:
				var role responses.EasyInputMessageRole
				switch msg.Role {
				case RoleUser:
					role = responses.EasyInputMessageRoleUser
				case RoleSystem:
					role = responses.EasyInputMessageRoleSystem // switch to developer?
				case RoleAssistant:
					role = responses.EasyInputMessageRoleAssistant
					content := []responses.ResponseOutputMessageContentUnionParam{
						{
							OfOutputText: &responses.ResponseOutputTextParam{
								Text: block.Text,
							},
						},
					}
					items = append(items, responses.ResponseInputItemParamOfOutputMessage(
						content,
						block.Id,
						responses.ResponseOutputMessageStatusCompleted,
					))
					continue contentBlocksLoop

				default:
					return nil, fmt.Errorf("unsupported role %s for text block", msg.Role)
				}

				// user or system role only here, as it's an "input" item
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
				if block.Reasoning != nil {
					reasoning := responses.ResponseReasoningItemParam{ID: block.Id}
					if block.Reasoning.Text != "" {
						reasoning.Content = append(reasoning.Content, responses.ResponseReasoningItemContentParam{
							Text: block.Reasoning.Text,
						})
					}

					reasoning.Summary = []responses.ResponseReasoningItemSummaryParam{}
					if block.Reasoning.Summary != "" {
						reasoning.Summary = append(reasoning.Summary, responses.ResponseReasoningItemSummaryParam{
							Text: block.Reasoning.Summary,
						})
					}

					if block.Reasoning.EncryptedContent != "" {
						reasoning.EncryptedContent = param.NewOpt(block.Reasoning.EncryptedContent)
					}

					reasoningItem := responses.ResponseInputItemUnionParam{OfReasoning: &reasoning}
					items = append(items, reasoningItem)
				} else {
					return nil, fmt.Errorf("reasoning block missing seasoning data: %s", utils.PanicJSON(block))
				}

			case ContentBlockTypeRefusal:
				// NOTE: refusals aren't represented in openai's input params,
				// we're working around it basically here to try to keep the
				// conversation going, as we don't have business logic to handle
				// refusals yet. Later, this could be considered a bad request
				// that returns a client-side validation error to disallow such
				// inputs.
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

func accumulateOpenaiEventsToMessage(events []Event) Message {
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
				// it's not a delta actually, it's the full encrypted content...
				block.Reasoning.EncryptedContent = evt.Delta
			}

		case EventBlockDone:
			if evt.ContentBlock != nil {
				if block, ok := blocks[evt.Index]; ok {
					if block.Type == ContentBlockTypeReasoning && evt.ContentBlock.Type == ContentBlockTypeReasoning {
						if evt.ContentBlock.Reasoning != nil {
							if block.Reasoning == nil {
								block.Reasoning = &ReasoningBlock{}
							}
							if evt.ContentBlock.Reasoning.Text != "" {
								block.Reasoning.Text = evt.ContentBlock.Reasoning.Text
							}
							if evt.ContentBlock.Reasoning.Summary != "" {
								block.Reasoning.Summary = evt.ContentBlock.Reasoning.Summary
							}
							if evt.ContentBlock.Reasoning.EncryptedContent != "" {
								block.Reasoning.EncryptedContent = evt.ContentBlock.Reasoning.EncryptedContent
							}
						}
					}
				}
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
