package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.temporal.io/sdk/activity"
)

const anthropicDefaultModel = "claude-sonnet-4-5"
const anthropicDefaultMaxTokens = 8000

type AnthropicResponsesProvider struct{}

func (p AnthropicResponsesProvider) Stream(ctx context.Context, options Options, eventChan chan<- Event) (*MessageResponse, error) {
	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
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

	secretName := fmt.Sprintf("%s_API_KEY", options.Params.ModelConfig.NormalizedProviderName())
	token, err := options.Secrets.SecretManager.GetSecret(secretName)
	if err != nil {
		return nil, err
	}

	client := anthropic.NewClient(option.WithAPIKey(token))

	model := options.Params.Model
	if model == "" {
		model = anthropicDefaultModel
	}

	effectiveMaxTokens := anthropicDefaultMaxTokens
	if options.Params.MaxTokens > 0 {
		effectiveMaxTokens = options.Params.MaxTokens
	}
	if modelInfo, ok := common.GetModel(options.Params.Provider, model); ok && modelInfo.Limit.Output > 0 {
		if effectiveMaxTokens == 0 || effectiveMaxTokens > modelInfo.Limit.Output {
			effectiveMaxTokens = modelInfo.Limit.Output
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.F(model),
		MaxTokens: anthropic.Int(int64(effectiveMaxTokens)),
	}

	if options.Params.Temperature != nil {
		params.Temperature = anthropic.F(float64(*options.Params.Temperature))
	}

	messages, err := messagesToAnthropicParams(options.Params.Messages)
	if err != nil {
		return nil, err
	}
	params.Messages = anthropic.F(messages)

	if len(options.Params.Tools) > 0 {
		tools, err := toolsToAnthropicParams(options.Params.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = anthropic.F(tools)

		toolChoice := toolChoiceToAnthropicParam(options.Params.ToolChoice, options.Params.ParallelToolCalls != nil && *options.Params.ParallelToolCalls)
		params.ToolChoice = anthropic.F(toolChoice)
	}

	stream := client.Messages.NewStreaming(ctx, params)

	var finalMessage anthropic.Message
	var events []Event
	nextBlockIndex := 0
	blockIndexMap := make(map[int64]int)
	startedBlocks := 0
	stoppedBlocks := 0

	for stream.Next() {
		event := stream.Current()

		err := finalMessage.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate message: %w", err)
		}

		switch evt := event.AsUnion().(type) {
		case anthropic.ContentBlockStartEvent:
			blockIndexMap[evt.Index] = nextBlockIndex
			var contentBlock ContentBlock

			switch evt.ContentBlock.Type {
			case "text":
				contentBlock = ContentBlock{
					Type: ContentBlockTypeText,
					Text: "",
				}
			case "tool_use":
				contentBlock = ContentBlock{
					Type: ContentBlockTypeToolUse,
					ToolUse: &ToolUseBlock{
						Id:        evt.ContentBlock.ID,
						Name:      evt.ContentBlock.Name,
						Arguments: "",
					},
				}
			default:
				return nil, fmt.Errorf("unsupported content block type in start event: %s", evt.ContentBlock.Type)
			}

			ev := Event{
				Type:         EventBlockStarted,
				Index:        nextBlockIndex,
				ContentBlock: &contentBlock,
			}
			events = append(events, ev)
			eventChan <- ev
			nextBlockIndex++
			startedBlocks++

		case anthropic.ContentBlockDeltaEvent:
			blockIndex, ok := blockIndexMap[evt.Index]
			if !ok {
				return nil, fmt.Errorf("received delta for unknown block index %d", evt.Index)
			}

			switch delta := evt.Delta.AsUnion().(type) {
			case anthropic.TextDelta:
				ev := Event{
					Type:  EventTextDelta,
					Index: blockIndex,
					Delta: delta.Text,
				}
				events = append(events, ev)
				eventChan <- ev

			case anthropic.InputJSONDelta:
				ev := Event{
					Type:  EventTextDelta,
					Index: blockIndex,
					Delta: delta.PartialJSON,
				}
				events = append(events, ev)
				eventChan <- ev
			}

		case anthropic.ContentBlockStopEvent:
			blockIndex, ok := blockIndexMap[evt.Index]
			if !ok {
				return nil, fmt.Errorf("received stop for unknown block index %d", evt.Index)
			}

			ev := Event{
				Type:  EventBlockDone,
				Index: blockIndex,
			}
			events = append(events, ev)
			eventChan <- ev
			stoppedBlocks++
		}
	}

	if stream.Err() != nil {
		return nil, stream.Err()
	}

	if startedBlocks != stoppedBlocks {
		return nil, fmt.Errorf("stream truncated: started %d blocks but stopped %d", startedBlocks, stoppedBlocks)
	}

	output := accumulateAnthropicEventsToMessage(events)

	responseModel := finalMessage.Model
	if responseModel == "" {
		responseModel = model
	}

	response := &MessageResponse{
		Id:           finalMessage.ID,
		Model:        responseModel,
		Provider:     options.Params.Provider,
		Output:       output,
		StopReason:   string(finalMessage.StopReason),
		StopSequence: finalMessage.StopSequence,
		Usage: Usage{
			InputTokens:  int(finalMessage.Usage.InputTokens),
			OutputTokens: int(finalMessage.Usage.OutputTokens),
		},
	}

	return response, nil
}

func accumulateAnthropicEventsToMessage(events []Event) Message {
	return accumulateOpenaiEventsToMessage(events)
}

func roleToAnthropicParam(role Role) anthropic.MessageParamRole {
	switch role {
	case RoleSystem, RoleUser:
		// anthropic doesn't have a system role
		return anthropic.MessageParamRoleUser
	case RoleAssistant:
		return anthropic.MessageParamRoleAssistant
	default:
		panic(fmt.Sprintf("unknown role: %s", role))
	}
}

func messagesToAnthropicParams(messages []Message) ([]anthropic.MessageParam, error) {
	var result []anthropic.MessageParam
	var currentRole anthropic.MessageParamRole
	var currentBlocks []anthropic.ContentBlockParamUnion

	flushCurrent := func() {
		if len(currentBlocks) > 0 {
			if currentRole == anthropic.MessageParamRoleUser {
				result = append(result, anthropic.NewUserMessage(currentBlocks...))
			} else {
				result = append(result, anthropic.NewAssistantMessage(currentBlocks...))
			}
			currentBlocks = nil
		}
	}

	for _, msg := range messages {
		if roleToAnthropicParam(msg.Role) != currentRole && len(currentBlocks) > 0 {
			flushCurrent()
		}
		currentRole = roleToAnthropicParam(msg.Role)

		for _, block := range msg.Content {
			anthropicBlock, err := contentBlockToAnthropicParam(block, msg.Role)
			if err != nil {
				return nil, err
			}
			currentBlocks = append(currentBlocks, anthropicBlock)
		}
	}

	flushCurrent()
	return result, nil
}

func contentBlockToAnthropicParam(block ContentBlock, role Role) (anthropic.ContentBlockParamUnion, error) {
	switch block.Type {
	case ContentBlockTypeText:
		textBlock := anthropic.NewTextBlock(block.Text)
		if block.CacheControl != "" {
			textBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
				Type: anthropic.F(anthropic.CacheControlEphemeralType(block.CacheControl)),
			})
		}
		return textBlock, nil

	case ContentBlockTypeToolUse:
		if role != RoleAssistant {
			return nil, fmt.Errorf("tool_use blocks only allowed in assistant messages")
		}
		if block.ToolUse == nil {
			return nil, fmt.Errorf("tool_use block missing ToolUse data")
		}

		var argsMap map[string]interface{}
		if block.ToolUse.Arguments != "" {
			if err := json.Unmarshal([]byte(block.ToolUse.Arguments), &argsMap); err != nil {
				argsMap = map[string]interface{}{
					"invalid_json_stringified": block.ToolUse.Arguments,
				}
			}
		} else {
			argsMap = make(map[string]interface{})
		}

		toolUseBlock := anthropic.NewToolUseBlockParam(block.ToolUse.Id, block.ToolUse.Name, argsMap)
		if block.CacheControl != "" {
			toolUseBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
				Type: anthropic.F(anthropic.CacheControlEphemeralType(block.CacheControl)),
			})
		}
		return toolUseBlock, nil

	case ContentBlockTypeToolResult:
		if role != RoleUser {
			return nil, fmt.Errorf("tool_result blocks only allowed in user messages")
		}
		if block.ToolResult == nil {
			return nil, fmt.Errorf("tool_result block missing ToolResult data")
		}

		toolResultBlock := anthropic.NewToolResultBlock(block.ToolResult.ToolCallId, block.ToolResult.Text, block.ToolResult.IsError)
		if block.CacheControl != "" {
			toolResultBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
				Type: anthropic.F(anthropic.CacheControlEphemeralType(block.CacheControl)),
			})
		}
		return toolResultBlock, nil

	case ContentBlockTypeRefusal:
		if role != RoleAssistant {
			return nil, fmt.Errorf("refusal blocks only allowed in assistant messages")
		}
		if block.Refusal == nil {
			return nil, fmt.Errorf("refusal block missing Refusal data")
		}
		textBlock := anthropic.NewTextBlock(block.Refusal.Reason)
		if block.CacheControl != "" {
			textBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
				Type: anthropic.F(anthropic.CacheControlEphemeralType(block.CacheControl)),
			})
		}
		return textBlock, nil

	case ContentBlockTypeReasoning:
		if role != RoleAssistant {
			return nil, fmt.Errorf("reasoning blocks only allowed in assistant messages")
		}
		if block.Reasoning == nil {
			return nil, fmt.Errorf("reasoning block missing Reasoning data")
		}
		textBlock := anthropic.NewTextBlock(block.Reasoning.Text)
		if block.CacheControl != "" {
			textBlock.CacheControl = anthropic.F(anthropic.CacheControlEphemeralParam{
				Type: anthropic.F(anthropic.CacheControlEphemeralType(block.CacheControl)),
			})
		}
		return textBlock, nil

	case ContentBlockTypeImage:
		return nil, fmt.Errorf("image blocks not yet supported")

	case ContentBlockTypeFile:
		return nil, fmt.Errorf("file blocks not yet supported")

	case ContentBlockTypeMcpCall:
		return nil, fmt.Errorf("mcp_call blocks not yet supported")

	default:
		return nil, fmt.Errorf("unsupported content block type: %s", block.Type)
	}
}

func toolsToAnthropicParams(tools []*common.Tool) ([]anthropic.ToolParam, error) {
	result := make([]anthropic.ToolParam, len(tools))
	for i, tool := range tools {
		result[i] = anthropic.ToolParam{
			Name:        anthropic.F(tool.Name),
			Description: anthropic.F(tool.Description),
			InputSchema: anthropic.F[interface{}](tool.Parameters),
		}
	}
	return result, nil
}

func toolChoiceToAnthropicParam(choice common.ToolChoice, parallelToolCalls bool) anthropic.ToolChoiceUnionParam {
	switch choice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		return anthropic.ToolChoiceAutoParam{
			Type:                   anthropic.F(anthropic.ToolChoiceAutoTypeAuto),
			DisableParallelToolUse: anthropic.F(!parallelToolCalls),
		}
	case common.ToolChoiceTypeRequired:
		return anthropic.ToolChoiceAnyParam{
			Type:                   anthropic.F(anthropic.ToolChoiceAnyTypeAny),
			DisableParallelToolUse: anthropic.F(!parallelToolCalls),
		}
	case common.ToolChoiceTypeTool:
		return anthropic.ToolChoiceToolParam{
			Type:                   anthropic.F(anthropic.ToolChoiceToolTypeTool),
			Name:                   anthropic.F(choice.Name),
			DisableParallelToolUse: anthropic.F(!parallelToolCalls),
		}
	default:
		return anthropic.ToolChoiceAutoParam{
			Type:                   anthropic.F(anthropic.ToolChoiceAutoTypeAuto),
			DisableParallelToolUse: anthropic.F(!parallelToolCalls),
		}
	}
}
