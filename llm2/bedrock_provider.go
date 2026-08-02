package llm2

import (
	"context"
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

	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(model),
		Messages:        messages,
		System:          system,
		InferenceConfig: bedrockInferenceConfig(request.Options),
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

	// TODO: surface reasoning content blocks once we plumb them through here.

	var collected []Event
	var stopReason string
	var usage Usage

	for evt := range stream.Events() {
		for _, mapped := range bedrockOutputToEvents(evt) {
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

	return &MessageResponse{
		Model:      model,
		Provider:   bedrockProviderName,
		Output:     output,
		StopReason: stopReason,
		Usage:      usage,
	}, nil
}

func bedrockInferenceConfig(opts Options) *types.InferenceConfiguration {
	cfg := &types.InferenceConfiguration{}
	set := false
	if opts.MaxTokens > 0 {
		cfg.MaxTokens = aws.Int32(int32(opts.MaxTokens))
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

func bedrockUsageFrom(u *types.TokenUsage) Usage {
	out := Usage{}
	if u == nil {
		return out
	}
	if u.InputTokens != nil {
		out.InputTokens = int(*u.InputTokens)
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
		switch blk.Type {
		case ContentBlockTypeText:
			if blk.Text == "" {
				continue
			}
			out = append(out, &types.ContentBlockMemberText{Value: blk.Text})
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
	}
	return out, nil
}

func bedrockFromLlm2ToolResultContent(blocks []ContentBlock) ([]types.ToolResultContentBlock, error) {
	var out []types.ToolResultContentBlock
	for _, blk := range blocks {
		if blk.Type == ContentBlockTypeText && blk.Text != "" {
			out = append(out, &types.ToolResultContentBlockMemberText{Value: blk.Text})
		}
	}
	if len(out) == 0 {
		// Bedrock requires at least one content entry for tool results.
		out = append(out, &types.ToolResultContentBlockMemberText{Value: ""})
	}
	return out, nil
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
		if !t.IsFunction() {
			return nil, fmt.Errorf("bedrock: provider-native tool type %q is not supported via Bedrock Converse", t.Type)
		}
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

func bedrockOutputToEvents(event types.ConverseStreamOutput) []Event {
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
		}
		return nil
	case *types.ConverseStreamOutputMemberContentBlockStop:
		idx := int(int32Deref(e.Value.ContentBlockIndex))
		return []Event{{Type: EventBlockDone, Index: idx}}
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
// text blocks as text_delta events and tool_use blocks as a block_started
// (with id/name) followed by text_delta events carrying JSON argument
// fragments, terminated by block_done. Mirrors accumulateGoogleEventsToMessage
// but scoped to the kinds Bedrock emits.
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
			}
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
