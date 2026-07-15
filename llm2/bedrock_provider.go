package llm2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sidekick/common"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
)

const (
	bedrockProviderName    = "bedrock"
	bedrockDefaultModel    = "global.anthropic.claude-haiku-4-5-20251001-v1:0"
	bedrockDefaultRegion   = "us-east-1"
	bedrockFallbackProfile = "personal"
)

// BedrockProvider streams responses from AWS Bedrock via the Converse API,
// which provides a unified abstraction across model families (Anthropic,
// Google Gemma, etc.).
type BedrockProvider struct {
	AuthType      common.ProviderAuthType
	Region        string
	Profile       string
	CustomHeaders map[string]string
}

func (p BedrockProvider) resolveProfile() string {
	if p.Profile != "" {
		return p.Profile
	}
	if env := os.Getenv("AWS_PROFILE"); env != "" {
		return env
	}
	return bedrockFallbackProfile
}

func (p BedrockProvider) resolveRegion() string {
	if p.Region != "" {
		return p.Region
	}
	if env := os.Getenv("AWS_REGION"); env != "" {
		return env
	}
	if env := os.Getenv("AWS_DEFAULT_REGION"); env != "" {
		return env
	}
	return bedrockDefaultRegion
}

func (p BedrockProvider) Stream(ctx context.Context, request StreamRequest, eventChan chan<- Event) (*MessageResponse, error) {
	model := request.Options.Model
	if model == "" {
		model = bedrockDefaultModel
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(p.resolveProfile()),
		awsconfig.WithRegion(p.resolveRegion()),
	)
	if err != nil {
		return nil, fmt.Errorf("bedrock: load AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(awsCfg)

	system, messages, err := bedrockFromLlm2Messages(request.Messages)
	if err != nil {
		return nil, err
	}

	if request.Options.Speed == anthropicSpeedFast {
		log.Debug().Str("model", model).Msg("bedrock: fast mode is not supported by the Converse API; ignoring speed option")
	}

	additionalFields, maxTokens, resolvedEffort := bedrockAnthropicRequestFields(request.Options, model)

	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(model),
		Messages:        messages,
		System:          system,
		InferenceConfig: bedrockInferenceConfig(request.Options, maxTokens),
	}
	if additionalFields != nil {
		input.AdditionalModelRequestFields = document.NewLazyDocument(additionalFields)
	}

	toolCfg, err := bedrockFromLlm2Tools(request.Options.Tools, request.Options.ToolChoice)
	if err != nil {
		return nil, err
	}
	if toolCfg != nil {
		input.ToolConfig = toolCfg
	}

	start := time.Now()
	streamOut, err := client.ConverseStream(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock: ConverseStream: %w", err)
	}
	stream := streamOut.GetStream()
	defer stream.Close()

	streamState := &bedrockStreamState{}

	var collected []Event
	var stopReason string
	var usage Usage

	for evt := range stream.Events() {
		for _, mapped := range bedrockOutputToEvents(evt, streamState) {
			collected = append(collected, mapped)
			select {
			case eventChan <- mapped:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		switch v := evt.(type) {
		case *types.ConverseStreamOutputMemberMessageStop:
			stopReason = string(v.Value.StopReason)
		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				usage = bedrockUsageFrom(v.Value.Usage)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("bedrock: stream: %w", err)
	}

	log.Debug().
		Str("provider", bedrockProviderName).
		Str("model", model).
		Dur("duration", time.Since(start)).
		Msg("bedrock stream complete")

	output := accumulateBedrockEventsToMessage(collected)

	// Report the resolved effort. When "lowest" resolved to "" (thinking off),
	// report "none" so consumers know thinking was intentionally skipped.
	reportedEffort := resolvedEffort
	if strings.Contains(strings.ToLower(model), "claude") &&
		request.Options.ReasoningEffort == "lowest" && resolvedEffort == "" {
		reportedEffort = "none"
	}

	return &MessageResponse{
		Model:           model,
		Provider:        bedrockProviderName,
		Output:          output,
		StopReason:      stopReason,
		Usage:           usage,
		AuthType:        common.ProviderAuthTypeAPI,
		ReasoningEffort: reportedEffort,
	}, nil
}

func bedrockInferenceConfig(opts Options, maxTokens int) *types.InferenceConfiguration {
	cfg := &types.InferenceConfiguration{}
	set := false
	if maxTokens > 0 {
		cfg.MaxTokens = aws.Int32(int32(maxTokens))
		set = true
	}
	if opts.Temperature != nil {
		cfg.Temperature = aws.Float32(*opts.Temperature)
		set = true
	}
	if !set {
		return nil
	}
	return cfg
}

// bedrockAnthropicRequestFields builds the Claude-specific extended/adaptive
// thinking configuration to pass via Converse AdditionalModelRequestFields,
// mirroring AnthropicProvider.Stream semantics: adaptive thinking with an
// effort output config on 4.6+ models, budget-based thinking otherwise, and
// no thinking when effort resolves to "" or tool_choice forces tool use.
//
// Capability gating per the AWS Bedrock docs ("Adaptive thinking" and
// "Extended thinking" under Anthropic Claude Messages API): thinking.type
// "adaptive" with thinking.display and a separate output_config.effort object
// is supported on Claude 4.6+ models (which anthropicSupportsAdaptiveThinking
// gates on), while 4.5-generation and older models only accept budget-based
// {"type": "enabled", "budget_tokens": N}, which is the fallback here.
// Anthropic-API-only features without a Converse equivalent (anthropic_beta
// values such as interleaved thinking, fast mode / the speed param) are
// intentionally omitted: adaptive thinking enables interleaved thinking
// automatically, and passing those fields would fail on some Bedrock models.
//
// Returns nil fields for non-Claude models. The returned maxTokens is
// options.MaxTokens, raised when needed so that max_tokens > budget_tokens.
func bedrockAnthropicRequestFields(options Options, model string) (fields map[string]any, maxTokens int, resolvedEffort string) {
	maxTokens = options.MaxTokens
	if !strings.Contains(strings.ToLower(model), "claude") {
		return nil, maxTokens, ""
	}

	resolvedEffort = resolveAnthropicReasoningEffort(options.ReasoningEffort, model)

	// Anthropic does not allow thinking when tool_choice forces tool use
	forcesTool := options.ToolChoice.Type == common.ToolChoiceTypeRequired || options.ToolChoice.Type == common.ToolChoiceTypeTool
	if forcesTool {
		if resolvedEffort != "" {
			log.Info().
				Str("model", model).
				Str("toolChoiceType", string(options.ToolChoice.Type)).
				Msg("disabling thinking because tool_choice forces tool use")
		}
		return nil, maxTokens, resolvedEffort
	}

	// Display must be set explicitly: some models default to "omitted", which
	// redacts thinking text to signature-only blocks.
	adaptiveThinking := map[string]any{"type": "adaptive", "display": "summarized"}

	if anthropicSupportsAdaptiveThinking(model) && resolvedEffort != "" {
		// Adaptive-capable models: thinking and effort are orthogonal.
		return map[string]any{
			"thinking":      adaptiveThinking,
			"output_config": map[string]any{"effort": resolvedEffort},
		}, maxTokens, resolvedEffort
	}
	if anthropicSupportsAdaptiveThinking(model) && resolvedEffort == "" && options.ReasoningEffort == "" {
		// Adaptive-capable model with no explicit effort: enable adaptive thinking at defaults.
		return map[string]any{"thinking": adaptiveThinking}, maxTokens, resolvedEffort
	}
	if resolvedEffort == "" {
		// "lowest" resolved to "": thinking is intentionally skipped.
		return nil, maxTokens, resolvedEffort
	}

	// Non-adaptive models (or adaptive with future effort levels):
	// use budget-based thinking.
	budgetTokens := 10000 // default for unrecognized effort levels
	switch resolvedEffort {
	case "low":
		budgetTokens = 5000
	case "medium":
		budgetTokens = 10000
	case "high":
		budgetTokens = 20000
	case "max":
		return map[string]any{"thinking": adaptiveThinking}, maxTokens, resolvedEffort
	}
	// max_tokens must be greater than thinking.budget_tokens; the Converse
	// default max tokens can be lower than typical budgets, so always set it
	// when budget-based thinking is enabled.
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	if maxTokens <= budgetTokens {
		maxTokens = budgetTokens + 1000
	}
	return map[string]any{
		"thinking": map[string]any{"type": "enabled", "budget_tokens": budgetTokens},
	}, maxTokens, resolvedEffort
}

func bedrockUsageFrom(u *types.TokenUsage) Usage {
	out := Usage{}
	if u == nil {
		return out
	}
	if u.OutputTokens != nil {
		out.OutputTokens = int(*u.OutputTokens)
	}
	if u.CacheReadInputTokens != nil {
		out.CacheReadInputTokens = int(*u.CacheReadInputTokens)
	}
	if u.CacheWriteInputTokens != nil {
		out.CacheWriteInputTokens = int(*u.CacheWriteInputTokens)
	}
	// Converse reports non-cached tokens in InputTokens; per AWS docs, the
	// total prompt token count is the sum of all three fields. Matches the
	// Anthropic provider's InputTokens semantics.
	if u.InputTokens != nil {
		out.InputTokens = int(*u.InputTokens)
	}
	out.InputTokens += out.CacheReadInputTokens + out.CacheWriteInputTokens
	return out
}

func bedrockFromLlm2Messages(messages []Message) ([]types.SystemContentBlock, []types.Message, error) {
	var system []types.SystemContentBlock
	var out []types.Message
	for _, m := range messages {
		if m.Role == RoleSystem {
			for _, blk := range m.Content {
				if blk.Type == ContentBlockTypeText && blk.Text != "" {
					system = append(system, &types.SystemContentBlockMemberText{Value: blk.Text})
					if blk.CacheControl != "" {
						system = append(system, &types.SystemContentBlockMemberCachePoint{
							Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
						})
					}
				}
			}
			continue
		}

		var role types.ConversationRole
		switch m.Role {
		case RoleUser:
			role = types.ConversationRoleUser
		case RoleAssistant:
			role = types.ConversationRoleAssistant
		default:
			return nil, nil, fmt.Errorf("bedrock: unsupported role %q", m.Role)
		}

		content, err := bedrockFromLlm2Content(m.Content)
		if err != nil {
			return nil, nil, err
		}
		if len(content) == 0 {
			continue
		}
		out = append(out, types.Message{Role: role, Content: content})
	}
	return system, out, nil
}

func bedrockFromLlm2Content(blocks []ContentBlock) ([]types.ContentBlock, error) {
	var out []types.ContentBlock
	for _, blk := range blocks {
		lenBefore := len(out)
		switch blk.Type {
		case ContentBlockTypeText:
			if blk.Text == "" {
				continue
			}
			out = append(out, &types.ContentBlockMemberText{Value: blk.Text})
		case ContentBlockTypeRefusal:
			if blk.Refusal == nil || blk.Refusal.Reason == "" {
				continue
			}
			out = append(out, &types.ContentBlockMemberText{Value: blk.Refusal.Reason})
		case ContentBlockTypeReasoning:
			if blk.Reasoning == nil {
				continue
			}
			if blk.Reasoning.EncryptedContent != "" {
				redacted, err := base64.StdEncoding.DecodeString(blk.Reasoning.EncryptedContent)
				if err != nil {
					return nil, fmt.Errorf("bedrock: decoding redacted reasoning content: %w", err)
				}
				out = append(out, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberRedactedContent{Value: redacted},
				})
			} else {
				text := types.ReasoningTextBlock{Text: aws.String(blk.Reasoning.Text)}
				if len(blk.Reasoning.Signature) > 0 {
					text.Signature = aws.String(string(blk.Reasoning.Signature))
				}
				out = append(out, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberReasoningText{Value: text},
				})
			}
		case ContentBlockTypeImage:
			if blk.Image == nil || blk.Image.Url == "" {
				return nil, fmt.Errorf("bedrock: image block missing ImageRef or URL")
			}
			img, err := bedrockImageBlockFromURL(blk.Image.Url)
			if err != nil {
				return nil, err
			}
			out = append(out, &types.ContentBlockMemberImage{Value: *img})
		case ContentBlockTypeToolUse:
			if blk.ToolUse == nil {
				continue
			}
			inputDoc, err := bedrockDocumentFromJSON(blk.ToolUse.Arguments)
			if err != nil {
				return nil, fmt.Errorf("bedrock: tool_use arguments: %w", err)
			}
			out = append(out, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: aws.String(blk.ToolUse.Id),
				Name:      aws.String(blk.ToolUse.Name),
				Input:     inputDoc,
			}})
		case ContentBlockTypeToolResult:
			if blk.ToolResult == nil {
				continue
			}
			resultContent, err := bedrockFromLlm2ToolResultContent(blk.ToolResult.Content)
			if err != nil {
				return nil, err
			}
			status := types.ToolResultStatusSuccess
			if blk.ToolResult.IsError {
				status = types.ToolResultStatusError
			}
			out = append(out, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
				ToolUseId: aws.String(blk.ToolResult.ToolCallId),
				Content:   resultContent,
				Status:    status,
			}})
		}
		if blk.CacheControl != "" && len(out) > lenBefore {
			out = append(out, &types.ContentBlockMemberCachePoint{
				Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
			})
		}
	}
	return out, nil
}

// bedrockFromLlm2ToolResultContent converts nested tool_result content.
// CacheControl on nested blocks is intentionally ignored: the Converse
// ToolResultContentBlock union has no cachePoint member, so cache control is
// only honored at the outer tool_result block level (see
// bedrockFromLlm2Content), matching the Anthropic provider.
func bedrockFromLlm2ToolResultContent(blocks []ContentBlock) ([]types.ToolResultContentBlock, error) {
	var out []types.ToolResultContentBlock
	for _, blk := range blocks {
		switch blk.Type {
		case ContentBlockTypeText:
			if blk.Text != "" {
				out = append(out, &types.ToolResultContentBlockMemberText{Value: blk.Text})
			}
		case ContentBlockTypeImage:
			if blk.Image == nil || blk.Image.Url == "" {
				return nil, fmt.Errorf("bedrock: nested image block in tool_result missing ImageRef or URL")
			}
			img, err := bedrockImageBlockFromURL(blk.Image.Url)
			if err != nil {
				return nil, err
			}
			out = append(out, &types.ToolResultContentBlockMemberImage{Value: *img})
		}
	}
	if len(out) == 0 {
		// Bedrock requires at least one content entry for tool results.
		out = append(out, &types.ToolResultContentBlockMemberText{Value: ""})
	}
	return out, nil
}

// bedrockImageBlockFromURL converts an image data URL into a Converse image
// block. The Converse API only accepts inline image bytes, so remote http(s)
// URLs are not supported.
func bedrockImageBlockFromURL(url string) (*types.ImageBlock, error) {
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return nil, fmt.Errorf("bedrock: remote image URLs are not supported by the Converse API, only data URLs")
	}

	// Converse limits images to 3.75 MB; the long-edge limit matches the
	// Anthropic provider since these images target Claude models.
	const bedrockMaxImageBytes = 3*1024*1024 + 768*1024
	const bedrockMaxLongEdgePx = 1568
	newDataURL, mime, _, err := PrepareImageDataURLForLimits(url, bedrockMaxImageBytes, bedrockMaxLongEdgePx)
	if err != nil {
		return nil, fmt.Errorf("bedrock: preparing image: %w", err)
	}

	_, raw, err := ParseDataURL(newDataURL)
	if err != nil {
		return nil, fmt.Errorf("bedrock: re-parsing prepared image data URL: %w", err)
	}

	return &types.ImageBlock{
		Format: types.ImageFormat(strings.TrimPrefix(mime, "image/")),
		Source: &types.ImageSourceMemberBytes{Value: raw},
	}, nil
}

func bedrockDocumentFromJSON(raw string) (document.Interface, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return document.NewLazyDocument(map[string]any{}), nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return document.NewLazyDocument(v), nil
}

func bedrockFromLlm2Tools(tools []*common.Tool, choice common.ToolChoice) (*types.ToolConfiguration, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	cfg := &types.ToolConfiguration{}
	for _, t := range tools {
		schemaDoc, err := bedrockToolSchemaDocument(t.Parameters)
		if err != nil {
			return nil, fmt.Errorf("bedrock: tool %q schema: %w", t.Name, err)
		}
		spec := types.ToolSpecification{
			Name:        aws.String(t.Name),
			Description: aws.String(t.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: schemaDoc},
		}
		cfg.Tools = append(cfg.Tools, &types.ToolMemberToolSpec{Value: spec})
	}
	switch choice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		cfg.ToolChoice = &types.ToolChoiceMemberAuto{}
	case common.ToolChoiceTypeRequired:
		cfg.ToolChoice = &types.ToolChoiceMemberAny{}
	case common.ToolChoiceTypeTool:
		if choice.Name == "" {
			return nil, errors.New("bedrock: tool choice 'tool' requires a name")
		}
		cfg.ToolChoice = &types.ToolChoiceMemberTool{Value: types.SpecificToolChoice{Name: aws.String(choice.Name)}}
	}
	return cfg, nil
}

func bedrockToolSchemaDocument(schema *jsonschema.Schema) (document.Interface, error) {
	if schema == nil {
		return document.NewLazyDocument(map[string]any{"type": "object"}), nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if _, ok := m["type"]; !ok {
		m["type"] = "object"
	}
	return document.NewLazyDocument(m), nil
}

// bedrockStreamState tracks per-stream bookkeeping needed to synthesize
// events that the Converse stream does not emit directly: Bedrock sends no
// content block start event for reasoning blocks, so an EventBlockStarted is
// emitted on the first reasoning delta per block index. Redacted reasoning
// bytes are buffered per block index so the full payload can be emitted as a
// single base64 EncryptedContent fragment at block stop, since independently
// base64-encoded fragments would not concatenate into valid base64.
type bedrockStreamState struct {
	reasoningStarted map[int]bool
	redactedContent  map[int][]byte
}

func bedrockOutputToEvents(event types.ConverseStreamOutput, state *bedrockStreamState) []Event {
	switch e := event.(type) {
	case *types.ConverseStreamOutputMemberContentBlockStart:
		idx := int(int32Deref(e.Value.ContentBlockIndex))
		if tu, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
			return []Event{{
				Type:  EventBlockStarted,
				Index: idx,
				ContentBlock: &ContentBlock{
					Type: ContentBlockTypeToolUse,
					ToolUse: &ToolUseBlock{
						Id:   aws.ToString(tu.Value.ToolUseId),
						Name: aws.ToString(tu.Value.Name),
					},
				},
			}}
		}
		return nil
	case *types.ConverseStreamOutputMemberContentBlockDelta:
		idx := int(int32Deref(e.Value.ContentBlockIndex))
		switch d := e.Value.Delta.(type) {
		case *types.ContentBlockDeltaMemberText:
			return []Event{{Type: EventTextDelta, Index: idx, Delta: d.Value}}
		case *types.ContentBlockDeltaMemberToolUse:
			return []Event{{Type: EventTextDelta, Index: idx, Delta: aws.ToString(d.Value.Input)}}
		case *types.ContentBlockDeltaMemberReasoningContent:
			var events []Event
			if state.reasoningStarted == nil {
				state.reasoningStarted = make(map[int]bool)
			}
			if !state.reasoningStarted[idx] {
				state.reasoningStarted[idx] = true
				events = append(events, Event{
					Type:  EventBlockStarted,
					Index: idx,
					ContentBlock: &ContentBlock{
						Type:      ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{},
					},
				})
			}
			switch r := d.Value.(type) {
			case *types.ReasoningContentBlockDeltaMemberText:
				events = append(events, Event{Type: EventTextDelta, Index: idx, Delta: r.Value})
			case *types.ReasoningContentBlockDeltaMemberSignature:
				events = append(events, Event{Type: EventSignatureDelta, Index: idx, Signature: []byte(r.Value)})
			case *types.ReasoningContentBlockDeltaMemberRedactedContent:
				// Buffer the opaque redacted bytes; they are surfaced as a
				// single EncryptedContent fragment at block stop.
				if state.redactedContent == nil {
					state.redactedContent = make(map[int][]byte)
				}
				state.redactedContent[idx] = append(state.redactedContent[idx], r.Value...)
			}
			return events
		}
		return nil
	case *types.ConverseStreamOutputMemberContentBlockStop:
		idx := int(int32Deref(e.Value.ContentBlockIndex))
		var events []Event
		if raw, ok := state.redactedContent[idx]; ok {
			// Emit the redacted payload as a single signature_delta whose
			// Delta accumulates into Reasoning.EncryptedContent, matching the
			// documented EventSignatureDelta semantics used by other providers.
			events = append(events, Event{Type: EventSignatureDelta, Index: idx, Delta: base64.StdEncoding.EncodeToString(raw)})
			delete(state.redactedContent, idx)
		}
		return append(events, Event{Type: EventBlockDone, Index: idx})
	}
	return nil
}

func int32Deref(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// accumulateBedrockEventsToMessage reconstructs the final assistant message
// from the ordered Events emitted during a Bedrock stream. Bedrock streams
// text blocks as text_delta events, tool_use blocks as a block_started
// (with id/name) followed by text_delta events carrying JSON argument
// fragments, and reasoning blocks as a block_started followed by text_delta
// (thinking text) and signature_delta events (Signature carries the thinking
// signature, as in the Anthropic provider; Delta carries base64
// EncryptedContent fragments, as in the OpenAI Responses provider),
// terminated by block_done. Mirrors accumulateGoogleEventsToMessage but
// scoped to the kinds Bedrock emits.
func accumulateBedrockEventsToMessage(events []Event) Message {
	blocks := make(map[int]*ContentBlock)
	maxIndex := -1
	for _, evt := range events {
		if evt.Index > maxIndex {
			maxIndex = evt.Index
		}
		switch evt.Type {
		case EventBlockStarted:
			if evt.ContentBlock != nil {
				cb := *evt.ContentBlock
				if cb.ToolUse != nil {
					tu := *cb.ToolUse
					cb.ToolUse = &tu
				}
				if cb.Reasoning != nil {
					r := *cb.Reasoning
					cb.Reasoning = &r
				}
				blocks[evt.Index] = &cb
			}
		case EventTextDelta:
			block, ok := blocks[evt.Index]
			if !ok {
				block = &ContentBlock{Type: ContentBlockTypeText}
				blocks[evt.Index] = block
			}
			switch block.Type {
			case ContentBlockTypeText:
				block.Text += evt.Delta
			case ContentBlockTypeToolUse:
				if block.ToolUse == nil {
					block.ToolUse = &ToolUseBlock{}
				}
				block.ToolUse.Arguments += evt.Delta
			case ContentBlockTypeReasoning:
				if block.Reasoning == nil {
					block.Reasoning = &ReasoningBlock{}
				}
				block.Reasoning.Text += evt.Delta
			}
		case EventSignatureDelta:
			block, ok := blocks[evt.Index]
			if !ok || block.Type != ContentBlockTypeReasoning {
				break
			}
			if block.Reasoning == nil {
				block.Reasoning = &ReasoningBlock{}
			}
			if len(evt.Signature) > 0 {
				block.Reasoning.Signature = append(block.Reasoning.Signature, evt.Signature...)
			}
			block.Reasoning.EncryptedContent += evt.Delta
		case EventBlockDone:
			// No-op: tool_use Arguments accumulate via EventTextDelta;
			// completion is signaled by ordering, not by trailing payload.
		}
	}
	ordered := make([]ContentBlock, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		if b, ok := blocks[i]; ok {
			ordered = append(ordered, *b)
		}
	}
	return Message{Role: RoleAssistant, Content: ordered}
}
