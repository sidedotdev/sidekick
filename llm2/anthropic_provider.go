package llm2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sidekick/common"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/rs/zerolog/log"
)

const anthropicDefaultModel = "claude-opus-4-5"
const anthropicDefaultMaxTokens = 24000

const (
	anthropicAcceptHeaderValue                  = "application/json"
	anthropicDangerousBrowserAccessHeaderValue  = "true"
	anthropicClaudeCLIUserAgent                 = "claude-cli/2.0.65 (external, cli)"
	anthropicCLIAppHeaderValue                  = "cli"
	anthropicClaudeCodeBetaHeader               = "claude-code-20250219"
	anthropicOAuthBetaHeader                    = "oauth-2025-04-20"
	anthropicFineGrainedToolStreamingBetaHeader = "fine-grained-tool-streaming-2025-05-14"
	anthropicInterleavedThinkingBetaHeader      = "interleaved-thinking-2025-05-14"
	anthropicFastModeBetaHeader                 = "fast-mode-2026-02-01"
)

// anthropicSpeedFast is the value of the Anthropic Messages API "speed" parameter
// that enables fast mode. Empty means unset (fast mode off).
const anthropicSpeedFast = "fast"

type AnthropicProvider struct {
	BaseURL             string
	DefaultModel        string
	AnthropicCompatible bool
	AuthType            common.ProviderAuthType
	CustomHeaders       map[string]string
}

func anthropicBetaHeader(model string, useOAuth bool, tools []*common.Tool, assumeAnthropicModelNames, fastMode bool) string {
	parts := make([]string, 0, 5)
	if useOAuth {
		hasNativeMappedTools := false
		for _, tool := range tools {
			if tool == nil {
				continue
			}
			switch tool.Name {
			case "Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit", "Skill", "Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch":
				hasNativeMappedTools = true
			}
			if hasNativeMappedTools {
				break
			}
		}
		if hasNativeMappedTools {
			parts = append(parts, anthropicClaudeCodeBetaHeader)
		}
		parts = append(parts, anthropicOAuthBetaHeader)
	}
	parts = append(parts, anthropicFineGrainedToolStreamingBetaHeader)
	if !assumeAnthropicModelNames || !anthropicSupportsAdaptiveThinking(model) {
		parts = append(parts, anthropicInterleavedThinkingBetaHeader)
	}
	if fastMode {
		parts = append(parts, anthropicFastModeBetaHeader)
	}
	return strings.Join(parts, ",")
}

func anthropicRequestHeaders(model string, useOAuth bool, accessToken string, tools []*common.Tool, assumeAnthropicModelNames, fastMode bool) map[string]string {
	headers := map[string]string{
		"Accept": anthropicAcceptHeaderValue,
		"anthropic-dangerous-direct-browser-access": anthropicDangerousBrowserAccessHeaderValue,
		"anthropic-beta": anthropicBetaHeader(model, useOAuth, tools, assumeAnthropicModelNames, fastMode),
	}

	if useOAuth {
		headers["Authorization"] = "Bearer " + accessToken
		headers["User-Agent"] = anthropicClaudeCLIUserAgent
		headers["x-app"] = anthropicCLIAppHeaderValue
	}

	return headers
}

func accumulateAnthropicMessageMetadata(message *anthropic.Message, event anthropic.MessageStreamEventUnion) error {
	switch event.AsAny().(type) {
	case anthropic.MessageStartEvent, anthropic.MessageDeltaEvent:
		if err := message.Accumulate(event); err != nil {
			return fmt.Errorf("failed to accumulate message metadata: %w", err)
		}
	}
	return nil
}

func (p AnthropicProvider) Stream(ctx context.Context, request StreamRequest, eventChan chan<- Event) (*MessageResponse, error) {
	messages := request.Messages
	options := request.Options

	model := options.Model
	if model == "" {
		model = p.DefaultModel
		if model == "" {
			model = anthropicDefaultModel
		}
	}
	assumeAnthropicModelNames := !p.AnthropicCompatible

	oauthCreds, token, useOAuth, err := anthropicCredentialsForRequest(
		request.SecretManager,
		options.ModelConfig.NormalizedProviderName(),
		p.AuthType,
	)
	if err != nil {
		return nil, err
	}

	var client anthropic.Client
	httpClient := &http.Client{Timeout: 45 * time.Minute}
	clientOptions := []option.RequestOption{
		option.WithHTTPClient(httpClient),
	}
	if p.BaseURL != "" {
		clientOptions = append(clientOptions, option.WithBaseURL(p.BaseURL))
	}
	for k, v := range p.CustomHeaders {
		clientOptions = append(clientOptions, option.WithHeader(k, v))
	}
	fastMode := options.Speed == anthropicSpeedFast

	if useOAuth {
		headers := anthropicRequestHeaders(model, true, oauthCreds.AccessToken, options.Tools, assumeAnthropicModelNames, fastMode)
		clientOptions = append(
			clientOptions,
			option.WithHeader("Authorization", headers["Authorization"]),
			option.WithHeader("Accept", headers["Accept"]),
			option.WithHeader("User-Agent", headers["User-Agent"]),
			option.WithHeader("x-app", headers["x-app"]),
			option.WithHeader("anthropic-dangerous-direct-browser-access", headers["anthropic-dangerous-direct-browser-access"]),
			option.WithHeader("anthropic-beta", headers["anthropic-beta"]),
		)
		client = newAnthropicClient(clientOptions...)
	} else {
		headers := anthropicRequestHeaders(model, false, "", options.Tools, assumeAnthropicModelNames, fastMode)
		clientOptions = append(
			clientOptions,
			option.WithAPIKey(token),
			option.WithHeader("Accept", headers["Accept"]),
			option.WithHeader("anthropic-dangerous-direct-browser-access", headers["anthropic-dangerous-direct-browser-access"]),
			option.WithHeader("anthropic-beta", headers["anthropic-beta"]),
		)
		client = newAnthropicClient(clientOptions...)
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
		// Display must be set explicitly: some models (e.g. fable) default to
		// "omitted", which redacts thinking and narration text to
		// signature-only blocks.
		adaptive := anthropic.ThinkingConfigAdaptiveParam{
			Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(resolvedEffort),
		}
	} else if anthropicSupportsAdaptiveThinking(model) && resolvedEffort == "" && options.ReasoningEffort == "" {
		// Adaptive-capable model with no explicit effort: enable adaptive thinking at defaults.
		adaptive := anthropic.ThinkingConfigAdaptiveParam{
			Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
		}
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
			adaptive := anthropic.ThinkingConfigAdaptiveParam{
				Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
			}
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		} else {
			// max_tokens must be greater than thinking.budget_tokens
			if int64(effectiveMaxTokens) <= budgetTokens {
				effectiveMaxTokens = int(budgetTokens) + 1000
				params.MaxTokens = int64(effectiveMaxTokens)
			}
			enabled := anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: budgetTokens,
				Display:      anthropic.ThinkingConfigEnabledDisplaySummarized,
			}
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &enabled}
		}
	}
	// When resolvedEffort is "" and ReasoningEffort was "lowest", thinking is
	// intentionally skipped (no params.Thinking set).

	var streamOpts []option.RequestOption
	if fastMode && strings.Contains(model, "opus-4-8") {
		// The non-beta MessageNewParams struct does not yet expose a Speed field
		// in the SDK; inject it into the request body directly so we stay on
		// client.Messages.NewStreaming.
		streamOpts = append(streamOpts, option.WithJSONSet("speed", anthropicSpeedFast))
	}
	stream := client.Messages.NewStreaming(ctx, params, streamOpts...)

	var finalMessage anthropic.Message
	var events []Event
	nextBlockIndex := 0
	blockIndexMap := make(map[int64]int)
	startedBlocks := 0
	stoppedBlocks := 0

	for stream.Next() {
		event := stream.Current()

		if err := accumulateAnthropicMessageMetadata(&finalMessage, event); err != nil {
			return nil, err
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

	authType := common.ProviderAuthTypeAPI
	if useOAuth {
		authType = common.ProviderAuthTypeSubscription
	}

	response := &MessageResponse{
		Id:              finalMessage.ID,
		Model:           responseModel,
		Provider:        options.Provider,
		Output:          output,
		StopReason:      string(finalMessage.StopReason),
		StopSequence:    finalMessage.StopSequence,
		Usage:           usage,
		AuthType:        authType,
		ReasoningEffort: reportedEffort,
	}

	return response, nil
}

func resolveAnthropicReasoningEffort(effort, model string) string {
	if effort != "lowest" && effort != "highest" {
		return effort
	}

	// Anthropic-compatible endpoints can proxy GPT models, such as LiteLLM routing OpenAI models through its Anthropic API.
	if resolvedEffort, ok := resolveGPTReasoningEffort(effort, model); ok {
		return resolvedEffort
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
// thinking should be enabled by default (version 4.6+, excluding haiku).
func anthropicSupportsAdaptiveThinking(model string) bool {
	// Haiku models don't support adaptive thinking regardless of version.
	if strings.Contains(strings.ToLower(model), "haiku") {
		return false
	}
	major, minor, ok := parseAnthropicVersion(model)
	if !ok {
		return false
	}
	// Adaptive thinking is supported starting from version 4.6.
	return major > 4 || (major == 4 && minor >= 6)
}

// parseAnthropicVersion extracts the major and minor version from an Anthropic
// model name, e.g. "claude-opus-4-6" or "claude-legendary-5.1-latest". The
// version is taken from the first numeric segments in the name, so unknown
// future model families are supported. Segments longer than two digits (e.g.
// date stamps like "20250514") are not treated as version numbers. Returns
// false if the model is not a claude model or no version can be found.
func parseAnthropicVersion(model string) (major, minor int, ok bool) {
	m := strings.ToLower(model)

	idx := strings.Index(m, "claude-")
	if idx < 0 {
		return 0, 0, false
	}
	rest := m[idx+len("claude-"):]

	isVersionSegment := func(s string) bool {
		if len(s) == 0 || len(s) > 2 {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}

	segments := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '-' || r == '.'
	})
	for i, seg := range segments {
		if !isVersionSegment(seg) {
			continue
		}
		major, _ = strconv.Atoi(seg)
		if i+1 < len(segments) && isVersionSegment(segments[i+1]) {
			minor, _ = strconv.Atoi(segments[i+1])
		}
		return major, minor, true
	}
	return 0, 0, false
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
