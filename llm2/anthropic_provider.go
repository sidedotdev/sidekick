package llm2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

const anthropicDefaultModel = "claude-opus-4-5"
const anthropicDefaultMaxTokens = 16000

type AnthropicProvider struct{}

func (p AnthropicProvider) Stream(ctx context.Context, request StreamRequest, eventChan chan<- Event) (*MessageResponse, error) {
	messages := request.Messages
	options := request.Options

	// Try OAuth credentials first, fall back to API key
	oauthCreds, useOAuth, err := llm.GetAnthropicOAuthCredentials(request.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	var client anthropic.Client
	httpClient := &http.Client{Timeout: 45 * time.Minute}
	if useOAuth {
		client = anthropic.NewClient(
			option.WithHTTPClient(httpClient),
			option.WithHeader("Authorization", "Bearer "+oauthCreds.AccessToken),
			option.WithHeader("anthropic-beta", llm.AnthropicOAuthBetaHeaders),
		)
	} else {
		secretName := fmt.Sprintf("%s_API_KEY", options.Params.ModelConfig.NormalizedProviderName())
		token, err := request.SecretManager.GetSecret(secretName)
		if err != nil {
			return nil, err
		}
		client = anthropic.NewClient(
			option.WithHTTPClient(httpClient),
			option.WithAPIKey(token),
		)
	}

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
		Model:     anthropic.Model(model),
		MaxTokens: int64(effectiveMaxTokens),
	}

	if options.Params.Temperature != nil {
		params.Temperature = anthropic.Opt(float64(*options.Params.Temperature))
	}

	if options.Params.ServiceTier != "" {
		params.ServiceTier = anthropic.MessageNewParamsServiceTier(options.Params.ServiceTier)
	}

	anthropicMessages, err := messagesToAnthropicParams(messages)
	if err != nil {
		return nil, err
	}
	params.Messages = anthropicMessages

	if len(options.Params.Tools) > 0 {
		tools, err := toolsToAnthropicParams(options.Params.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = tools

		toolChoice := toolChoiceToAnthropicParam(options.Params.ToolChoice, options.Params.ParallelToolCalls != nil && *options.Params.ParallelToolCalls)
		params.ToolChoice = toolChoice
	}

	if useOAuth {
		// NOTE: OAuth tokens require using the Claude Code system prompt, otherwise you get a 400 error
		var systemMessages []anthropic.TextBlockParam
		systemMessages = append(systemMessages, anthropic.TextBlockParam{Text: "You are Claude Code, Anthropic's official CLI for Claude."})
		params.System = systemMessages
	}

	// Enable extended thinking if reasoning effort is specified
	if options.Params.ReasoningEffort != "" {
		budgetTokens := int64(10000) // default
		switch options.Params.ReasoningEffort {
		case "low":
			budgetTokens = 5000
		case "medium":
			budgetTokens = 10000
		case "high":
			budgetTokens = 20000
		}
		// max_tokens must be greater than thinking.budget_tokens
		if int64(effectiveMaxTokens) <= budgetTokens {
			effectiveMaxTokens = int(budgetTokens) + 1000
			params.MaxTokens = int64(effectiveMaxTokens)
		}
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budgetTokens)
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

		switch evt := event.AsAny().(type) {
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
			case "thinking":
				contentBlock = ContentBlock{
					Type: ContentBlockTypeReasoning,
					Reasoning: &ReasoningBlock{
						Text: "",
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

			switch delta := evt.Delta.AsAny().(type) {
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

			case anthropic.ThinkingDelta:
				ev := Event{
					Type:  EventTextDelta,
					Index: blockIndex,
					Delta: delta.Thinking,
				}
				events = append(events, ev)
				eventChan <- ev

			case anthropic.SignatureDelta:
				ev := Event{
					Type:      EventSignatureDelta,
					Index:     blockIndex,
					Signature: []byte(delta.Signature),
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

	responseModel := string(finalMessage.Model)
	if responseModel == "" {
		responseModel = model
	}

	// Anthropic returns non-cached tokens as InputTokens; the total prompt
	// token count is the sum of all three fields.
	usage := Usage{
		InputTokens:           int(finalMessage.Usage.InputTokens) + int(finalMessage.Usage.CacheReadInputTokens) + int(finalMessage.Usage.CacheCreationInputTokens),
		OutputTokens:          int(finalMessage.Usage.OutputTokens),
		CacheReadInputTokens:  int(finalMessage.Usage.CacheReadInputTokens),
		CacheWriteInputTokens: int(finalMessage.Usage.CacheCreationInputTokens),
	}

	response := &MessageResponse{
		Id:           finalMessage.ID,
		Model:        responseModel,
		Provider:     options.Params.Provider,
		Output:       output,
		StopReason:   string(finalMessage.StopReason),
		StopSequence: finalMessage.StopSequence,
		Usage:        usage,
	}

	return response, nil
}

func accumulateAnthropicEventsToMessage(events []Event) Message {
	msg := Message{
		Role:    RoleAssistant,
		Content: []ContentBlock{},
	}

	for _, event := range events {
		switch event.Type {
		case EventBlockStarted:
			if event.ContentBlock != nil {
				block := *event.ContentBlock
				msg.Content = append(msg.Content, block)
			}
		case EventTextDelta:
			if event.Index < len(msg.Content) {
				block := &msg.Content[event.Index]
				switch block.Type {
				case ContentBlockTypeText:
					block.Text += event.Delta
				case ContentBlockTypeToolUse:
					if block.ToolUse != nil {
						block.ToolUse.Arguments += event.Delta
					}
				case ContentBlockTypeReasoning:
					if block.Reasoning != nil {
						block.Reasoning.Text += event.Delta
					}
				case ContentBlockTypeRefusal:
					if block.Refusal != nil {
						block.Refusal.Reason += event.Delta
					}
				}
			}
		case EventSummaryTextDelta:
			if event.Index < len(msg.Content) {
				block := &msg.Content[event.Index]
				if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Summary += event.Delta
				}
			}
		case EventSignatureDelta:
			if event.Index < len(msg.Content) {
				block := &msg.Content[event.Index]
				if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Signature = append(block.Reasoning.Signature, event.Signature...)
				}
			}
		}
	}

	return msg
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

func toolResultImageToAnthropicParam(url string) (*anthropic.ImageBlockParam, error) {
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return &anthropic.ImageBlockParam{
			Source: anthropic.ImageBlockParamSourceUnion{
				OfURL: &anthropic.URLImageSourceParam{
					URL:  url,
					Type: "url",
				},
			},
		}, nil
	}

	const anthropicMaxBytes = 30 * 1024 * 1024
	const anthropicMaxLongEdgePx = 1568
	newDataURL, mime, _, err := PrepareImageDataURLForLimits(url, anthropicMaxBytes, anthropicMaxLongEdgePx)
	if err != nil {
		return nil, fmt.Errorf("preparing image for Anthropic tool_result: %w", err)
	}

	_, raw, err := ParseDataURL(newDataURL)
	if err != nil {
		return nil, fmt.Errorf("re-parsing prepared image data URL: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	return &anthropic.ImageBlockParam{
		Source: anthropic.ImageBlockParamSourceUnion{
			OfBase64: &anthropic.Base64ImageSourceParam{
				MediaType: anthropic.Base64ImageSourceMediaType(mime),
				Data:      encoded,
				Type:      "base64",
			},
		},
	}, nil
}

func contentBlockToAnthropicParam(block ContentBlock, role Role) (anthropic.ContentBlockParamUnion, error) {
	switch block.Type {
	case ContentBlockTypeText:
		textBlock := anthropic.NewTextBlock(block.Text)
		if block.CacheControl != "" {
			textBlock.OfText.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return textBlock, nil

	case ContentBlockTypeToolUse:
		if role != RoleAssistant {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("tool_use blocks only allowed in assistant messages")
		}
		if block.ToolUse == nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("tool_use block missing ToolUse data")
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

		toolUseBlock := anthropic.ContentBlockParamUnion{
			OfToolUse: &anthropic.ToolUseBlockParam{
				ID:    block.ToolUse.Id,
				Name:  block.ToolUse.Name,
				Input: argsMap,
			},
		}
		if block.CacheControl != "" {
			toolUseBlock.OfToolUse.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return toolUseBlock, nil

	case ContentBlockTypeToolResult:
		if role != RoleUser {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("tool_result blocks only allowed in user messages")
		}
		if block.ToolResult == nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("tool_result block missing ToolResult data")
		}

		var contentParts []anthropic.ToolResultBlockParamContentUnion

		for _, nested := range block.ToolResult.Content {
			switch nested.Type {
			case ContentBlockTypeText:
				contentParts = append(contentParts, anthropic.ToolResultBlockParamContentUnion{
					OfText: &anthropic.TextBlockParam{Text: nested.Text},
				})
			case ContentBlockTypeImage:
				if nested.Image == nil || nested.Image.Url == "" {
					return anthropic.ContentBlockParamUnion{}, fmt.Errorf("nested image block in tool_result missing ImageRef or URL")
				}
				imgParam, err := toolResultImageToAnthropicParam(nested.Image.Url)
				if err != nil {
					return anthropic.ContentBlockParamUnion{}, err
				}
				contentParts = append(contentParts, anthropic.ToolResultBlockParamContentUnion{
					OfImage: imgParam,
				})
			default:
				return anthropic.ContentBlockParamUnion{}, fmt.Errorf("unsupported nested content block type in tool_result: %s", nested.Type)
			}
		}

		if len(contentParts) == 0 {
			contentParts = append(contentParts, anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: ""},
			})
		}

		toolResultBlock := anthropic.ContentBlockParamUnion{
			OfToolResult: &anthropic.ToolResultBlockParam{
				ToolUseID: block.ToolResult.ToolCallId,
				Content:   contentParts,
				IsError:   anthropic.Bool(block.ToolResult.IsError),
			},
		}
		if block.CacheControl != "" {
			toolResultBlock.OfToolResult.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return toolResultBlock, nil

	case ContentBlockTypeRefusal:
		if role != RoleAssistant {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("refusal blocks only allowed in assistant messages")
		}
		if block.Refusal == nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("refusal block missing Refusal data")
		}
		textBlock := anthropic.NewTextBlock(block.Refusal.Reason)
		if block.CacheControl != "" {
			textBlock.OfText.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return textBlock, nil

	case ContentBlockTypeReasoning:
		if role != RoleAssistant {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("reasoning blocks only allowed in assistant messages")
		}
		if block.Reasoning == nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("reasoning block missing Reasoning data")
		}
		textBlock := anthropic.NewTextBlock(block.Reasoning.Text)
		if block.CacheControl != "" {
			textBlock.OfText.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return textBlock, nil

	case ContentBlockTypeImage:
		if block.Image == nil || block.Image.Url == "" {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("image block missing ImageRef or URL")
		}
		url := block.Image.Url

		if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
			imgBlock := anthropic.NewImageBlock(anthropic.URLImageSourceParam{
				URL:  url,
				Type: "url",
			})
			if block.CacheControl != "" {
				imgBlock.OfImage.CacheControl = anthropic.CacheControlEphemeralParam{
					Type: "ephemeral",
				}
			}
			return imgBlock, nil
		}

		// data: URL — resize/recompress within Anthropic limits.
		const anthropicMaxBytes = 30 * 1024 * 1024 // 30 MB conservative limit
		const anthropicMaxLongEdgePx = 1568
		newDataURL, mime, _, err := PrepareImageDataURLForLimits(url, anthropicMaxBytes, anthropicMaxLongEdgePx)
		if err != nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("preparing image for Anthropic: %w", err)
		}

		_, raw, err := ParseDataURL(newDataURL)
		if err != nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("re-parsing prepared image data URL: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(raw)

		imgBlock := anthropic.NewImageBlockBase64(mime, encoded)
		if block.CacheControl != "" {
			imgBlock.OfImage.CacheControl = anthropic.CacheControlEphemeralParam{
				Type: "ephemeral",
			}
		}
		return imgBlock, nil

	case ContentBlockTypeFile:
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("file blocks not yet supported")

	case ContentBlockTypeMcpCall:
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("mcp_call blocks not yet supported")

	default:
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("unsupported content block type: %s", block.Type)
	}
}

func toolsToAnthropicParams(tools []*common.Tool) ([]anthropic.ToolUnionParam, error) {
	result := make([]anthropic.ToolUnionParam, len(tools))
	for i, tool := range tools {
		result[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.Opt(tool.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties:  tool.Parameters.Properties,
					Required:    tool.Parameters.Required,
					Type:        constant.Object(tool.Parameters.Type),
					ExtraFields: tool.Parameters.Extras,
				},
			},
		}
	}
	return result, nil
}

func toolChoiceToAnthropicParam(choice common.ToolChoice, parallelToolCalls bool) anthropic.ToolChoiceUnionParam {
	switch choice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		return anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{
				DisableParallelToolUse: anthropic.Opt(!parallelToolCalls),
			},
		}
	case common.ToolChoiceTypeRequired:
		return anthropic.ToolChoiceUnionParam{
			OfAny: &anthropic.ToolChoiceAnyParam{
				DisableParallelToolUse: anthropic.Opt(!parallelToolCalls),
			},
		}
	case common.ToolChoiceTypeTool:
		return anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{
				Name:                   choice.Name,
				DisableParallelToolUse: anthropic.Opt(!parallelToolCalls),
			},
		}
	default:
		return anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{
				DisableParallelToolUse: anthropic.Opt(!parallelToolCalls),
			},
		}
	}
}
