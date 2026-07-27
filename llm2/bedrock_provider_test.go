package llm2

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sidekick/common"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func isBedrockAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"UnrecognizedClient",
		"InvalidSignature",
		"InvalidClientTokenId",
		"AccessDenied",
		"NotAuthorized",
		"403",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func TestBedrockProvider_Unauthorized(t *testing.T) {
	// Point shared-config/credentials at empty files so the SDK can't pick
	// up real credentials from the developer's machine; that way the
	// (invalid) env credentials below are what actually sign the request.
	emptyDir := t.TempDir()
	emptyConfig := emptyDir + "/config"
	emptyCreds := emptyDir + "/credentials"
	if err := os.WriteFile(emptyConfig, []byte("[default]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if err := os.WriteFile(emptyCreds, []byte("[default]\naws_access_key_id = not-a-real-key\naws_secret_access_key = not-a-real-secret\n"), 0o600); err != nil {
		t.Fatalf("write empty credentials: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", emptyConfig)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", emptyCreds)
	t.Setenv("AWS_ACCESS_KEY_ID", "not-a-real-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "not-a-real-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "default")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")

	ctx := context.Background()
	provider := BedrockProvider{
		Region:  "us-east-1",
		Profile: "default",
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Hello",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "global.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
	}

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, request, eventChan)
	assert.Error(t, err)
	if err != nil {
		assert.Truef(t, isBedrockAuthError(err),
			"expected auth-related error, got: %v", err)
	}
}

func TestBedrockProvider_Integration_Gemma(t *testing.T) {
	t.Parallel()
	profile := requireAWSCredentialsForIntegration(t)

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := BedrockProvider{Profile: profile}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Say hi.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "google.gemma-3-27b-it",
		},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawTextDelta bool

	fmt.Println("\n=== Bedrock Provider Integration Test (Gemma) ===")

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			switch event.Type {
			case EventTextDelta:
				sawTextDelta = true
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}
		}
	}()

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.NotEmpty(t, allEvents, "Expected at least one event")
	assert.True(t, sawTextDelta, "Expected at least one EventTextDelta")

	var accumulatedText string
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeText {
			accumulatedText += block.Text
		}
	}
	assert.NotEmpty(t, strings.TrimSpace(accumulatedText),
		"Expected non-empty accumulated text in response.Output.Content")

	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d",
		response.Usage.InputTokens, response.Usage.OutputTokens)
}

func TestBedrockProvider_Integration_ClaudeHaiku(t *testing.T) {
	t.Parallel()
	profile := requireAWSCredentialsForIntegration(t)

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := BedrockProvider{Profile: profile}

	mockTool := &common.Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Look up the current weather in New York in celsius using the provided tool.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "global.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
		Tools:      []*common.Tool{mockTool},
		ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawToolUseBlockEvent bool

	fmt.Println("\n=== Bedrock Provider Integration Test (Claude Haiku) ===")

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			switch event.Type {
			case EventBlockStarted:
				blockType := ""
				if event.ContentBlock != nil {
					blockType = string(event.ContentBlock.Type)
					if event.ContentBlock.Type == ContentBlockTypeToolUse {
						sawToolUseBlockEvent = true
					}
				}
				fmt.Printf("Event[%d]: type=block_started block_type=%s\n", event.Index, blockType)
			case EventTextDelta:
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			case EventBlockDone:
				if event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
					sawToolUseBlockEvent = true
				}
				fmt.Printf("Event[%d]: type=block_done\n", event.Index)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}
		}
	}()

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.NotEmpty(t, allEvents, "Expected at least one event")
	assert.True(t, sawToolUseBlockEvent,
		"Expected to see a block_started or block_done event with ContentBlockTypeToolUse")

	var foundWeatherToolUse bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil &&
			block.ToolUse.Name == "get_current_weather" {
			foundWeatherToolUse = true
			t.Logf("Found tool_use block: id=%s name=%s args=%s",
				block.ToolUse.Id, block.ToolUse.Name, block.ToolUse.Arguments)
		}
	}
	assert.True(t, foundWeatherToolUse,
		"Expected response.Output.Content to include a get_current_weather tool_use block")

	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d",
		response.Usage.InputTokens, response.Usage.OutputTokens)
}

func TestBedrockAnthropicRequestFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		model         string
		effort        string
		toolChoice    common.ToolChoice
		maxTokens     int
		wantFields    map[string]any
		wantMaxTokens int
		wantEffort    string
	}{
		{
			name:   "adaptive model with explicit effort",
			model:  "global.anthropic.claude-opus-4-6-v1",
			effort: "medium",
			wantFields: map[string]any{
				"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
				"output_config": map[string]any{"effort": "medium"},
			},
			wantEffort: "medium",
		},
		{
			name:  "adaptive model default effort",
			model: "us.anthropic.claude-sonnet-4-6",
			wantFields: map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
			},
		},
		{
			name:   "adaptive model highest effort maps to max",
			model:  "global.anthropic.claude-opus-4-8",
			effort: "highest",
			wantFields: map[string]any{
				"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
				"output_config": map[string]any{"effort": "max"},
			},
			wantEffort: "max",
		},
		{
			name:      "budget thinking on haiku bumps max tokens above budget",
			model:     "global.anthropic.claude-haiku-4-5-20251001-v1:0",
			effort:    "high",
			maxTokens: 8000,
			wantFields: map[string]any{
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 20000},
			},
			wantMaxTokens: 21000,
			wantEffort:    "high",
		},
		{
			name:   "budget thinking defaults max tokens when unset",
			model:  "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			effort: "medium",
			wantFields: map[string]any{
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 10000},
			},
			wantMaxTokens: anthropicDefaultMaxTokens,
			wantEffort:    "medium",
		},
		{
			name:   "budget thinking low effort",
			model:  "eu.anthropic.claude-opus-4-5-20251101-v1:0",
			effort: "low",
			wantFields: map[string]any{
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 5000},
			},
			wantMaxTokens: anthropicDefaultMaxTokens,
			wantEffort:    "low",
		},
		{
			name:       "lowest effort disables thinking",
			model:      "global.anthropic.claude-opus-4-6-v1",
			effort:     "lowest",
			wantFields: nil,
		},
		{
			name:       "tool choice required disables thinking",
			model:      "global.anthropic.claude-opus-4-6-v1",
			effort:     "high",
			toolChoice: common.ToolChoice{Type: common.ToolChoiceTypeRequired},
			wantFields: nil,
			wantEffort: "high",
		},
		{
			name:       "specific tool choice disables thinking",
			model:      "global.anthropic.claude-haiku-4-5-20251001-v1:0",
			effort:     "high",
			toolChoice: common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "get_current_weather"},
			wantFields: nil,
			wantEffort: "high",
		},
		{
			name:       "non-claude model gets no anthropic fields",
			model:      "google.gemma-3-27b-it",
			effort:     "high",
			maxTokens:  4000,
			wantFields: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := Options{
				ModelConfig: common.ModelConfig{
					Provider:        "bedrock",
					Model:           tt.model,
					ReasoningEffort: tt.effort,
				},
				ToolChoice: tt.toolChoice,
				MaxTokens:  tt.maxTokens,
			}
			fields, maxTokens, resolvedEffort := bedrockAnthropicRequestFields(opts, tt.model)
			assert.Equal(t, tt.wantFields, fields)
			assert.Equal(t, tt.wantEffort, resolvedEffort)
			if tt.wantMaxTokens != 0 {
				assert.Equal(t, tt.wantMaxTokens, maxTokens)
			} else {
				assert.Equal(t, tt.maxTokens, maxTokens)
			}
		})
	}
}

// Per the llm2.Usage contract, InputTokens is the total prompt token count;
// Converse reports non-cached tokens in InputTokens, so cache read/write
// tokens must be added in (matching the Anthropic provider's semantics).
func TestBedrockUsageFrom(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Usage{}, bedrockUsageFrom(nil))

	usage := bedrockUsageFrom(&types.TokenUsage{
		InputTokens:           aws.Int32(100),
		OutputTokens:          aws.Int32(40),
		CacheReadInputTokens:  aws.Int32(300),
		CacheWriteInputTokens: aws.Int32(50),
	})
	assert.Equal(t, Usage{
		InputTokens:           450,
		OutputTokens:          40,
		CacheReadInputTokens:  300,
		CacheWriteInputTokens: 50,
	}, usage)

	// Without cache activity, InputTokens passes through unchanged.
	usage = bedrockUsageFrom(&types.TokenUsage{
		InputTokens:  aws.Int32(100),
		OutputTokens: aws.Int32(40),
	})
	assert.Equal(t, Usage{InputTokens: 100, OutputTokens: 40}, usage)
}

func TestBedrockOutputToEvents_Reasoning(t *testing.T) {
	t.Parallel()

	state := &bedrockStreamState{}
	deltaEvent := func(idx int32, delta types.ReasoningContentBlockDelta) types.ConverseStreamOutput {
		return &types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(idx),
				Delta:             &types.ContentBlockDeltaMemberReasoningContent{Value: delta},
			},
		}
	}

	events := bedrockOutputToEvents(deltaEvent(0, &types.ReasoningContentBlockDeltaMemberText{Value: "thinking "}), state)
	if assert.Len(t, events, 2) {
		assert.Equal(t, EventBlockStarted, events[0].Type)
		if assert.NotNil(t, events[0].ContentBlock) {
			assert.Equal(t, ContentBlockTypeReasoning, events[0].ContentBlock.Type)
			assert.NotNil(t, events[0].ContentBlock.Reasoning)
		}
		assert.Equal(t, EventTextDelta, events[1].Type)
		assert.Equal(t, "thinking ", events[1].Delta)
	}

	// Subsequent deltas for the same block don't re-emit block_started.
	events = bedrockOutputToEvents(deltaEvent(0, &types.ReasoningContentBlockDeltaMemberText{Value: "more"}), state)
	if assert.Len(t, events, 1) {
		assert.Equal(t, EventTextDelta, events[0].Type)
		assert.Equal(t, "more", events[0].Delta)
	}

	events = bedrockOutputToEvents(deltaEvent(0, &types.ReasoningContentBlockDeltaMemberSignature{Value: "sig123"}), state)
	if assert.Len(t, events, 1) {
		assert.Equal(t, EventSignatureDelta, events[0].Type)
		assert.Equal(t, []byte("sig123"), events[0].Signature)
	}

	// Redacted reasoning at a new index starts its own reasoning block; the
	// opaque bytes are buffered across fragments and surfaced as one
	// signature_delta whose Delta is the base64 EncryptedContent.
	events = bedrockOutputToEvents(deltaEvent(1, &types.ReasoningContentBlockDeltaMemberRedactedContent{Value: []byte{0x01, 0x02}}), state)
	if assert.Len(t, events, 1) {
		assert.Equal(t, EventBlockStarted, events[0].Type)
		if assert.NotNil(t, events[0].ContentBlock) {
			assert.Equal(t, ContentBlockTypeReasoning, events[0].ContentBlock.Type)
		}
	}
	events = bedrockOutputToEvents(deltaEvent(1, &types.ReasoningContentBlockDeltaMemberRedactedContent{Value: []byte{0x03}}), state)
	assert.Empty(t, events)

	events = bedrockOutputToEvents(&types.ConverseStreamOutputMemberContentBlockStop{
		Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)},
	}, state)
	if assert.Len(t, events, 2) {
		assert.Equal(t, EventSignatureDelta, events[0].Type)
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}), events[0].Delta)
		assert.Equal(t, EventBlockDone, events[1].Type)
	}
}

func TestAccumulateBedrockEventsToMessage_Reasoning(t *testing.T) {
	t.Parallel()

	events := []Event{
		{Type: EventBlockStarted, Index: 0, ContentBlock: &ContentBlock{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{}}},
		{Type: EventTextDelta, Index: 0, Delta: "step one, "},
		{Type: EventTextDelta, Index: 0, Delta: "step two"},
		{Type: EventSignatureDelta, Index: 0, Signature: []byte("sig123")},
		{Type: EventBlockDone, Index: 0},
		{Type: EventBlockStarted, Index: 1, ContentBlock: &ContentBlock{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{}}},
		{Type: EventSignatureDelta, Index: 1, Delta: base64.StdEncoding.EncodeToString([]byte("redacted-bytes"))},
		{Type: EventBlockDone, Index: 1},
		{Type: EventTextDelta, Index: 2, Delta: "The answer is 42."},
		{Type: EventBlockDone, Index: 2},
	}

	msg := accumulateBedrockEventsToMessage(events)
	if !assert.Len(t, msg.Content, 3) {
		return
	}

	reasoning := msg.Content[0]
	assert.Equal(t, ContentBlockTypeReasoning, reasoning.Type)
	if assert.NotNil(t, reasoning.Reasoning) {
		assert.Equal(t, "step one, step two", reasoning.Reasoning.Text)
		assert.Equal(t, []byte("sig123"), reasoning.Reasoning.Signature)
		assert.Empty(t, reasoning.Reasoning.EncryptedContent)
	}

	redacted := msg.Content[1]
	assert.Equal(t, ContentBlockTypeReasoning, redacted.Type)
	if assert.NotNil(t, redacted.Reasoning) {
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("redacted-bytes")), redacted.Reasoning.EncryptedContent)
		assert.Empty(t, redacted.Reasoning.Text)
	}

	assert.Equal(t, ContentBlockTypeText, msg.Content[2].Type)
	assert.Equal(t, "The answer is 42.", msg.Content[2].Text)
}

func TestBedrockFromLlm2Content_ReasoningRefusalAndCachePoints(t *testing.T) {
	t.Parallel()

	blocks := []ContentBlock{
		{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "thought", Signature: []byte("sig")}},
		{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{EncryptedContent: base64.StdEncoding.EncodeToString([]byte("opaque"))}},
		{Type: ContentBlockTypeRefusal, Refusal: &RefusalBlock{Reason: "cannot help"}},
		{Type: ContentBlockTypeText, Text: "hello", CacheControl: "ephemeral"},
	}

	out, err := bedrockFromLlm2Content(blocks)
	assert.NoError(t, err)
	if !assert.Len(t, out, 5) {
		return
	}

	reasoning, ok := out[0].(*types.ContentBlockMemberReasoningContent)
	if assert.True(t, ok) {
		text, ok := reasoning.Value.(*types.ReasoningContentBlockMemberReasoningText)
		if assert.True(t, ok) {
			assert.Equal(t, "thought", aws.ToString(text.Value.Text))
			assert.Equal(t, "sig", aws.ToString(text.Value.Signature))
		}
	}

	redacted, ok := out[1].(*types.ContentBlockMemberReasoningContent)
	if assert.True(t, ok) {
		content, ok := redacted.Value.(*types.ReasoningContentBlockMemberRedactedContent)
		if assert.True(t, ok) {
			assert.Equal(t, []byte("opaque"), content.Value)
		}
	}

	refusal, ok := out[2].(*types.ContentBlockMemberText)
	if assert.True(t, ok) {
		assert.Equal(t, "cannot help", refusal.Value)
	}

	text, ok := out[3].(*types.ContentBlockMemberText)
	if assert.True(t, ok) {
		assert.Equal(t, "hello", text.Value)
	}
	cachePoint, ok := out[4].(*types.ContentBlockMemberCachePoint)
	if assert.True(t, ok) {
		assert.Equal(t, types.CachePointTypeDefault, cachePoint.Value.Type)
	}
}

func TestBedrockProvider_Integration_ClaudeHaikuThinking(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}
	profile := requireAWSCredentialsForIntegration(t)

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := BedrockProvider{Profile: profile}

	reasoningModel := os.Getenv("BEDROCK_REASONING_MODEL")
	if reasoningModel == "" {
		reasoningModel = "global.anthropic.claude-haiku-4-5-20251001-v1:0"
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "What is 27 * 43? Reason carefully before answering, then state the final number.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider:        "bedrock",
			Model:           reasoningModel,
			ReasoningEffort: "medium",
		},
	}

	eventChan := make(chan Event, 100)
	consumerDone := make(chan struct{})
	var allEvents []Event
	var sawReasoningBlockStart bool

	fmt.Printf("\n=== Bedrock Provider Integration Test (Claude Thinking, %s) ===\n", reasoningModel)

	go func() {
		defer close(consumerDone)
		for event := range eventChan {
			allEvents = append(allEvents, event)
			switch event.Type {
			case EventBlockStarted:
				blockType := ""
				if event.ContentBlock != nil {
					blockType = string(event.ContentBlock.Type)
					if event.ContentBlock.Type == ContentBlockTypeReasoning {
						sawReasoningBlockStart = true
					}
				}
				fmt.Printf("Event[%d]: type=block_started block_type=%s\n", event.Index, blockType)
			case EventTextDelta:
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}
		}
	}()

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)
	<-consumerDone

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.NotEmpty(t, allEvents, "Expected at least one event")
	assert.True(t, sawReasoningBlockStart,
		"Expected a block_started event with ContentBlockTypeReasoning")

	var reasoningText, answerText string
	var sawReasoningSignature bool
	for _, block := range response.Output.Content {
		switch block.Type {
		case ContentBlockTypeReasoning:
			if block.Reasoning != nil {
				reasoningText += block.Reasoning.Text
				if len(block.Reasoning.Signature) > 0 {
					sawReasoningSignature = true
				}
			}
		case ContentBlockTypeText:
			answerText += block.Text
		}
	}
	assert.NotEmpty(t, strings.TrimSpace(reasoningText),
		"Expected non-empty accumulated reasoning text in response.Output.Content")
	assert.True(t, sawReasoningSignature, "Expected a reasoning block with a signature")
	assert.NotEmpty(t, strings.TrimSpace(answerText),
		"Expected non-empty accumulated answer text in response.Output.Content")
	assert.Contains(t, strings.ReplaceAll(answerText, ",", ""), "1161")
	assert.Equal(t, "medium", response.ReasoningEffort)

	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("Reasoning: %s", reasoningText)
	t.Logf("Answer: %s", answerText)
}

func TestBedrockFromLlm2Content_DropsNonReplayableReasoning(t *testing.T) {
	t.Parallel()

	blocks := []ContentBlock{
		{
			Id:   "rs_abc123",
			Type: ContentBlockTypeReasoning,
			Reasoning: &ReasoningBlock{
				Summary:          "Multiplying step by step",
				EncryptedContent: "gAAAAABopenai-encrypted-reasoning",
			},
		},
		{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "unsigned thinking"}},
		{Type: ContentBlockTypeText, Text: "The answer is 44323."},
	}

	out, err := bedrockFromLlm2Content(blocks)
	assert.NoError(t, err)
	if assert.Len(t, out, 1) {
		text, ok := out[0].(*types.ContentBlockMemberText)
		if assert.True(t, ok) {
			assert.Equal(t, "The answer is 44323.", text.Value)
		}
	}
}
