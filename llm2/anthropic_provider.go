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
	"github.com/rs/zerolog/log"
)

const anthropicDefaultModel = "claude-opus-4-5"
const anthropicDefaultMaxTokens = 16000
const anthropicOAuthBetaHeaders = "oauth-2025-04-20,claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"

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
			option.WithHeader("Accept", "application/json"),
			option.WithHeader("User-Agent", "claude-cli/2.1.62"),
			option.WithHeader("x-app", "cli"),
			option.WithHeader("anthropic-dangerous-direct-browser-access", "true"),
			option.WithHeader("anthropic-beta", anthropicOAuthBetaHeaders),
		)
	} else {
		secretName := fmt.Sprintf("%s_API_KEY", options.ModelConfig.NormalizedProviderName())
		token, err := request.SecretManager.GetSecret(secretName)
		if err != nil {
			return nil, err
		}
		client = anthropic.NewClient(
			option.WithHTTPClient(httpClient),
			option.WithAPIKey(token),
		)
	}

	model := options.Model
	if model == "" {
		model = anthropicDefaultModel
	}

	effectiveMaxTokens := anthropicDefaultMaxTokens
	if options.MaxTokens > 0 {
		effectiveMaxTokens = options.MaxTokens
	}
	if modelInfo, ok := common.GetModel(options.Provider, model); ok && modelInfo.Limit.Output > 0 {
		if effectiveMaxTokens == 0 || effectiveMaxTokens > modelInfo.Limit.Output {
			effectiveMaxTokens = modelInfo.Limit.Output
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(effectiveMaxTokens),
	}

	if options.Temperature != nil {
		params.Temperature = anthropic.Opt(float64(*options.Temperature))
	}

	if options.ServiceTier != "" {
		params.ServiceTier = anthropic.MessageNewParamsServiceTier(options.ServiceTier)
	}

	anthropicMessages, err := messagesToAnthropicParams(messages)
	if err != nil {
		return nil, err
	}
	params.Messages = anthropicMessages

	if len(options.Tools) > 0 {
		tools, err := toolsToAnthropicParams(options.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = tools

		toolChoice := toolChoiceToAnthropicParam(options.ToolChoice, options.ParallelToolCalls != nil && *options.ParallelToolCalls)
		params.ToolChoice = toolChoice
	}

	if useOAuth {
		// NOTE: OAuth tokens require using the Claude Code system prompt, otherwise you get a 400 error
		var systemMessages []anthropic.TextBlockParam
		systemMessages = append(systemMessages, anthropic.TextBlockParam{Text: "You are Claude Code, Anthropic's official CLI for Claude."})
		params.System = systemMessages
	}

	resolvedEffort := resolveAnthropicReasoningEffort(options.ReasoningEffort, model)

	// Anthropic does not allow thinking when tool_choice forces tool use
	forcesTool := options.ToolChoice.Type == common.ToolChoiceTypeRequired || options.ToolChoice.Type == common.ToolChoiceTypeTool
	if forcesTool && resolvedEffort != "" {
		log.Info().
			Str("model", model).
			Str("toolChoiceType", string(options.ToolChoice.Type)).
			Msg("disabling thinking because tool_choice forces tool use")
	} else if anthropicSupportsAdaptiveThinking(model) && resolvedEffort != "" {
		// Adaptive-capable models: thinking and effort are orthogonal.
		// Enable adaptive thinking and set effort via OutputConfig.
		adaptive := anthropic.NewThinkingConfigAdaptiveParam()
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(resolvedEffort),
		}
	} else if anthropicSupportsAdaptiveThinking(model) && resolvedEffort == "" && options.ReasoningEffort == "" {
		// Adaptive-capable model with no explicit effort: enable adaptive thinking at defaults.
		adaptive := anthropic.NewThinkingConfigAdaptiveParam()
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	} else if resolvedEffort != "" {
		// Non-adaptive models (or adaptive with future effort levels):
		// use budget-based thinking.
		budgetTokens := int64(10000) // default for unrecognized effort levels
		useAdaptive := false
		switch resolvedEffort {
		case "low":
			budgetTokens = 5000
		case "medium":
			budgetTokens = 10000
		case "high":
			budgetTokens = 20000
		case "max":
			useAdaptive = true
		}
		if useAdaptive {
			adaptive := anthropic.NewThinkingConfigAdaptiveParam()
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		} else {
			// max_tokens must be greater than thinking.budget_tokens
			if int64(effectiveMaxTokens) <= budgetTokens {
				effectiveMaxTokens = int(budgetTokens) + 1000
				params.MaxTokens = int64(effectiveMaxTokens)
			}
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budgetTokens)
		}
	}
	// When resolvedEffort is "" and ReasoningEffort was "lowest", thinking is
	// intentionally skipped (no params.Thinking set).

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
				toolUseName := evt.ContentBlock.Name
				contentBlock = ContentBlock{
					Type: ContentBlockTypeToolUse,
					ToolUse: &ToolUseBlock{
						Id:        evt.ContentBlock.ID,
						Name:      toolUseName,
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

	// Report the resolved effort. When "lowest" resolved to "" (thinking off),
	// report "none" so consumers know thinking was intentionally skipped.
	reportedEffort := resolvedEffort
	if options.ReasoningEffort == "lowest" && resolvedEffort == "" {
		reportedEffort = "none"
	}

	response := &MessageResponse{
		Id:              finalMessage.ID,
		Model:           responseModel,
		Provider:        options.Provider,
		Output:          output,
		StopReason:      string(finalMessage.StopReason),
		StopSequence:    finalMessage.StopSequence,
		Usage:           usage,
		ReasoningEffort: reportedEffort,
	}

	return response, nil
}

func resolveAnthropicReasoningEffort(effort, model string) string {
	if effort != "lowest" && effort != "highest" {
		return effort
	}

	modelLower := strings.ToLower(model)
	if !strings.Contains(modelLower, "claude") {
		if effort == "lowest" {
			// Anthropic doesn't have a "none" effort; thinking is controlled separately from effort.
			// Returning "" is a reliable way to map "lowest" to "default effort + no thinking".
			return ""
		}
		return "high"
	}

	if effort == "lowest" {
		return ""
	}
	// highest
	if anthropicSupportsAdaptiveThinking(model) {
		return "max"
	}
	return "high"
}

// anthropicSupportsAdaptiveThinking returns true for models where adaptive
// thinking should be enabled by default (Opus and Sonnet 4.6+).
func anthropicSupportsAdaptiveThinking(model string) bool {
	major, minor, ok := parseAnthropicVersion(model)
	if !ok {
		return false
	}
	// Adaptive thinking is supported starting from version 4.6.
	return major > 4 || (major == 4 && minor >= 6)
}

// parseAnthropicVersion extracts the major and minor version from an Anthropic
// model name for the opus or sonnet family. Returns false if the model is not
// a recognized opus/sonnet model or the version cannot be parsed.
func parseAnthropicVersion(model string) (major, minor int, ok bool) {
	m := strings.ToLower(model)

	// Find the family prefix to locate where the version starts.
	var versionPart string
	for _, family := range []string{"opus-", "sonnet-"} {
		idx := strings.Index(m, family)
		if idx >= 0 {
			versionPart = m[idx+len(family):]
			break
		}
	}
	if versionPart == "" {
		return 0, 0, false
	}

	// Parse major version (digits at the start).
	i := 0
	for i < len(versionPart) && versionPart[i] >= '0' && versionPart[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, 0, false
	}
	major = 0
	for _, c := range versionPart[:i] {
		major = major*10 + int(c-'0')
	}

	// Parse optional minor version after '.' or '-'.
	if i < len(versionPart) && (versionPart[i] == '.' || versionPart[i] == '-') {
		rest := versionPart[i+1:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 0 {
			for _, c := range rest[:j] {
				minor = minor*10 + int(c-'0')
			}
		}
	}

	return major, minor, true
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

func roleToAnthropicParam(role Role) (anthropic.MessageParamRole, error) {
	switch role {
	case RoleSystem, RoleUser:
		// anthropic doesn't have a system role
		return anthropic.MessageParamRoleUser, nil
	case RoleAssistant:
		return anthropic.MessageParamRoleAssistant, nil
	default:
		return "", fmt.Errorf("unknown role: %s", role)
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
		msgRole, err := roleToAnthropicParam(msg.Role)
		if err != nil {
			return nil, err
		}
		if msgRole != currentRole && len(currentBlocks) > 0 {
			flushCurrent()
		}
		currentRole = msgRole

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
		if block.Reasoning.EncryptedContent != "" {
			return anthropic.NewRedactedThinkingBlock(block.Reasoning.EncryptedContent), nil
		}
		return anthropic.NewThinkingBlock(string(block.Reasoning.Signature), block.Reasoning.Text), nil

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
