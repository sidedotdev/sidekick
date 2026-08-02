package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestAnthropicResponsesProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := AnthropicProvider{}

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
			Provider: "anthropic",
			Model:    "claude-sonnet-4-5",
		},
	}

	request := StreamRequest{
		Messages:      messages,
		Options:       options,
		SecretManager: mockSecretManager,
	}

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, request, eventChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestAnthropicResponsesProvider_Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}

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
					Text: "First say hi. After that, then look up what the weather is like in New York in celsius. Let me know, then check London too for me.",
				},
			},
		},
	}

	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "",
		},
		Tools:      []*common.Tool{mockTool},
		ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawBlockStartedToolUse bool
	var sawTextDelta bool

	fmt.Println("\n=== Anthropic Provider Integration Test ===")

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			// Debug: print each event
			switch event.Type {
			case EventBlockStarted:
				blockType := ""
				if event.ContentBlock != nil {
					blockType = string(event.ContentBlock.Type)
				}
				fmt.Printf("Event[%d]: type=block_started block_type=%s\n", event.Index, blockType)
			case EventTextDelta:
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			case EventBlockDone:
				fmt.Printf("Event[%d]: type=block_done\n", event.Index)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}

			if event.Type == EventBlockStarted && event.ContentBlock.Type == ContentBlockTypeToolUse {
				sawBlockStartedToolUse = true
			}
			if event.Type == EventTextDelta {
				sawTextDelta = true
			}
		}
	}()

	request := StreamRequest{
		Messages:      messages,
		Options:       options,
		SecretManager: secretManager,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping test due to transient Anthropic API error: %v", err)
		}
		if isAnthropicCredentialError(err) {
			t.Skipf("Skipping test due to Anthropic credentials not configured/invalid: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	if len(allEvents) == 0 {
		t.Error("No events received")
	}

	if !sawBlockStartedToolUse && !sawTextDelta {
		t.Error("Expected to see at least one block_started event with tool_use or text_delta event")
	}

	t.Logf("Response output content blocks: %d", len(response.Output.Content))

	// Debug: print all content blocks
	fmt.Printf("\n=== All Content Blocks (total: %d) ===\n", len(response.Output.Content))
	for i, block := range response.Output.Content {
		fmt.Printf("Block[%d] Type=%s\n", i, block.Type)
		switch block.Type {
		case ContentBlockTypeText:
			textPreview := block.Text
			if len(textPreview) > 100 {
				textPreview = textPreview[:100] + "..."
			}
			fmt.Printf("  Text=%q\n", textPreview)
		case ContentBlockTypeToolUse:
			if block.ToolUse != nil {
				fmt.Printf("  ToolUse: ID=%s Name=%s ArgsLen=%d\n", block.ToolUse.Id, block.ToolUse.Name, len(block.ToolUse.Arguments))
			}
		case ContentBlockTypeReasoning:
			if block.Reasoning != nil {
				fmt.Printf("  Reasoning: TextLen=%d SummaryLen=%d\n", len(block.Reasoning.Text), len(block.Reasoning.Summary))
			}
		}
	}

	var foundToolUseOrText bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse {
			foundToolUseOrText = true
			if block.ToolUse.Name == "get_current_weather" {
				t.Logf("Found tool_use block: %+v", block.ToolUse)
			}
		}
		if block.Type == ContentBlockTypeText && block.Text != "" {
			foundToolUseOrText = true
		}
	}

	if !foundToolUseOrText {
		t.Error("Expected response.Output.Content to include a tool_use block or text content")
	}

	assert.NotEmpty(t, response.StopReason, "StopReason should not be empty")
	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)

	// Capture values for parallel subtests before parent returns
	parentResponse := response
	parentMessages := append([]Message{}, messages...)

	t.Run("MultiTurn", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		messages := append(parentMessages, parentResponse.Output)

		for _, block := range parentResponse.Output.Content {
			if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeToolResult,
							ToolResult: &ToolResultBlock{
								ToolCallId: block.ToolUse.Id,
								Content:    []ContentBlock{{Type: ContentBlockTypeText, Text: "25"}},
								IsError:    false,
							},
						},
					},
				})
			}
		}

		eventChan := make(chan Event, 100)
		var allEvents []Event

		go func() {
			for event := range eventChan {
				allEvents = append(allEvents, event)
			}
		}()

		request := StreamRequest{
			Messages:      messages,
			Options:       options,
			SecretManager: secretManager,
		}
		response, err := provider.Stream(ctx, request, eventChan)
		close(eventChan)

		if err != nil {
			if isAnthropicTransientError(err) {
				t.Skipf("Skipping multi-turn test due to transient Anthropic API error: %v", err)
			}
			if isAnthropicCredentialError(err) {
				t.Skipf("Skipping multi-turn test due to Anthropic credentials not configured/invalid: %v", err)
			}
			t.Fatalf("Stream returned an error: %v", err)
		}

		if response == nil {
			t.Fatal("Stream returned a nil response")
		}

		if len(allEvents) == 0 {
			t.Error("No events received")
		}

		t.Logf("Response output content blocks (multi-turn): %d", len(response.Output.Content))
		t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

		var hasContent bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				hasContent = true
				break
			}
			if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				hasContent = true
				break
			}
		}

		if !hasContent {
			t.Error("Response content is empty after providing tool results")
		}

		assert.NotNil(t, response.Usage, "Usage field should not be nil on multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})

}

func TestAnthropicResponsesProvider_ReasoningIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}
	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")

	reasoningModel := os.Getenv("ANTHROPIC_REASONING_MODEL")
	if reasoningModel == "" {
		reasoningModel = "claude-sonnet-4-5"
	}
	fmt.Printf("\n=== Reasoning Test (%s) ===\n", reasoningModel)

	reasoningMessages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "What is 127 * 349? Think through this step by step, showing your work.",
				},
			},
		},
	}

	reasoningOptions := Options{
		ModelConfig: common.ModelConfig{
			Provider:        "anthropic",
			Model:           reasoningModel,
			ReasoningEffort: "low",
			MaxTokens:       1024,
		},
	}

	eventChan := make(chan Event, 100)
	go func() {
		for range eventChan {
		}
	}()

	reasoningRequest := StreamRequest{
		Messages:      reasoningMessages,
		Options:       reasoningOptions,
		SecretManager: secretManager,
	}
	response, err := provider.Stream(ctx, reasoningRequest, eventChan)
	close(eventChan)

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping reasoning test due to transient Anthropic API error: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	var hasReasoning bool
	var hasText bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil && len(block.Reasoning.Text) > 0 {
			hasReasoning = true
			t.Logf("Reasoning text length: %d", len(block.Reasoning.Text))
		}
		if block.Type == ContentBlockTypeText && block.Text != "" {
			hasText = true
		}
	}

	if !hasReasoning {
		t.Log("No reasoning block found - model may not support extended thinking")
	}
	if !hasText {
		t.Error("Expected text content in response")
	}

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, StopReason: %s", response.Model, response.StopReason)
}

func TestAnthropicResponsesProvider_CacheControl(t *testing.T) {
	testCases := []struct {
		name        string
		message     Message
		expectError bool
	}{
		{
			name: "text block with cache control",
			message: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type:         ContentBlockTypeText,
						Text:         "Hello, world!",
						CacheControl: "ephemeral",
					},
				},
			},
			expectError: false,
		},
		{
			name: "tool_use block with cache control",
			message: Message{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        "test-tool-id",
							Name:      "test_tool",
							Arguments: `{"arg":"value"}`,
						},
						CacheControl: "ephemeral",
					},
				},
			},
			expectError: false,
		},
		{
			name: "tool_result block with cache control",
			message: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolResult,
						ToolResult: &ToolResultBlock{
							ToolCallId: "test-tool-id",
							Content:    []ContentBlock{{Type: ContentBlockTypeText, Text: "result text"}},
							IsError:    false,
						},
						CacheControl: "ephemeral",
					},
				},
			},
			expectError: false,
		},
		{
			name: "refusal block with cache control",
			message: Message{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeRefusal,
						Refusal: &RefusalBlock{
							Reason: "I cannot do that",
						},
						CacheControl: "ephemeral",
					},
				},
			},
			expectError: false,
		},
		{
			name: "reasoning block (cache control not supported by thinking blocks)",
			message: Message{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text:      "Let me think about this...",
							Summary:   "Thinking",
							Signature: []byte("test-signature"),
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := messagesToAnthropicParams([]Message{tc.message})
			if tc.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotEmpty(t, params)

			jsonBytes, err := json.Marshal(params)
			assert.NoError(t, err)

			jsonStr := string(jsonBytes)
			if tc.message.Content[0].CacheControl != "" {
				assert.Contains(t, jsonStr, `"cache_control":{"type":"ephemeral"}`,
					"Expected cache_control to be present in JSON output for %s", tc.name)
			}

			if tc.message.Content[0].Type == ContentBlockTypeReasoning {
				assert.Contains(t, jsonStr, `"thinking"`,
					"Expected thinking block type in JSON output for %s", tc.name)
			}
		})
	}
}

func TestAnthropicProvider_ImageIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}

	expectedText, dataURL := GenerateVisionTestImage(6)
	t.Logf("Generated vision test image with text: %q", expectedText)

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type:  ContentBlockTypeImage,
					Image: &ImageRef{Url: dataURL},
				},
				{
					Type: ContentBlockTypeText,
					Text: fmt.Sprintf("What text is written in this image? %s Reply with ONLY the exact text, nothing else.", VisionTestCharSetHint()),
				},
			},
		},
	}

	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
	}

	request := StreamRequest{
		Messages:      messages,
		Options:       options,
		SecretManager: secretManager,
	}

	eventChan := make(chan Event, 100)
	var fullText strings.Builder
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eventChan {
			if event.Type == EventTextDelta {
				fullText.WriteString(event.Delta)
			}
		}
	}()

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)
	wg.Wait()

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping test due to transient Anthropic API error: %v", err)
		}
		if isAnthropicCredentialError(err) {
			t.Skipf("Skipping test due to Anthropic credentials not configured/invalid: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	assert.NotNil(t, response)
	responseText := strings.TrimSpace(fullText.String())
	t.Logf("Model response: %q", responseText)
	assert.True(t, VisionTestFuzzyMatch(expectedText, responseText),
		"Expected model to read %q from the image, got %q", expectedText, responseText)
}

func TestAnthropicProvider_ToolResultImageIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}

	expectedText, dataURL := GenerateVisionTestImage(6)
	t.Logf("Generated vision test image with text: %q", expectedText)

	toolCallId := "tool_call_img_001"
	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: fmt.Sprintf("Please use the read_image tool to read the image at path 'test.png'. %s Reply with ONLY the exact text, nothing else.", VisionTestCharSetHint()),
				},
			},
		},
		{
			Role: RoleAssistant,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeToolUse,
					ToolUse: &ToolUseBlock{
						Id:        toolCallId,
						Name:      "read_image",
						Arguments: `{"file_path": "test.png"}`,
					},
				},
			},
		},
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeToolResult,
					ToolResult: &ToolResultBlock{
						ToolCallId: toolCallId,
						Name:       "read_image",
						Content: []ContentBlock{
							{Type: ContentBlockTypeText, Text: "Here is the image content:"},
							{
								Type:  ContentBlockTypeImage,
								Image: &ImageRef{Url: dataURL},
							},
						},
					},
				},
			},
		},
	}

	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
		Tools: []*common.Tool{
			{
				Name:        "read_image",
				Description: "Reads an image file and returns its content",
				Parameters: (&jsonschema.Reflector{DoNotReference: true}).Reflect(&struct {
					FilePath string `json:"file_path" jsonschema:"description=Path to the image file"`
				}{}),
			},
		},
	}

	request := StreamRequest{
		Messages:      messages,
		Options:       options,
		SecretManager: secretManager,
	}

	eventChan := make(chan Event, 100)
	var fullText strings.Builder
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eventChan {
			if event.Type == EventTextDelta {
				fullText.WriteString(event.Delta)
			}
		}
	}()

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)
	wg.Wait()

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping test due to transient Anthropic API error: %v", err)
		}
		if isAnthropicCredentialError(err) {
			t.Skipf("Skipping test due to Anthropic credentials not configured/invalid: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	assert.NotNil(t, response)
	responseText := strings.TrimSpace(fullText.String())
	t.Logf("Model response: %q", responseText)
	assert.True(t, VisionTestFuzzyMatch(expectedText, responseText),
		"Expected model to read %q from the image, got %q", expectedText, responseText)
}
func isAnthropicTransientError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return contains(errStr, "overloaded_error") ||
		contains(errStr, "Overloaded") ||
		contains(errStr, "rate_limit")
}
func isAnthropicCredentialError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return contains(errStr, "invalid_grant") ||
		contains(errStr, "Refresh token not found or invalid") ||
		contains(errStr, "failed to get Anthropic OAuth credentials") ||
		contains(errStr, "OAuth token has been revoked") ||
		contains(errStr, "401 Unauthorized")
}

func TestAnthropicResponsesProvider_FastModeIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAny}
	secretManager := requireIntegrationAPIKey(t, llm.AnthropicOAuthSecretName, "ANTHROPIC_API_KEY")

	fastModel := os.Getenv("ANTHROPIC_FAST_MODEL")
	if fastModel == "" {
		fastModel = "claude-opus-4-8"
	}
	fmt.Printf("\n=== Fast Mode Test (%s) ===\n", fastModel)

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Say hello in exactly one short sentence.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider:  "anthropic",
			Model:     fastModel,
			Speed:     "fast",
			MaxTokens: 256,
		},
	}

	eventChan := make(chan Event, 100)
	var sawTextDelta bool
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for event := range eventChan {
			if event.Type == EventTextDelta {
				sawTextDelta = true
			}
		}
	}()

	request := StreamRequest{
		Messages:      messages,
		Options:       options,
		SecretManager: secretManager,
	}
	start := time.Now()
	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)
	<-doneCh
	elapsed := time.Since(start)

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping fast-mode test due to transient Anthropic API error: %v", err)
		}
		if isAnthropicCredentialError(err) {
			t.Skipf("Skipping fast-mode test due to Anthropic credentials not configured/invalid: %v", err)
		}
		if contains(err.Error(), "does not support the `speed` parameter") {
			t.Skipf("Skipping fast-mode test: model %q does not support speed (set ANTHROPIC_FAST_MODEL to a supported model): %v", fastModel, err)
		}
		if contains(err.Error(), "Fast mode is not enabled") {
			t.Skipf("Skipping fast-mode test: fast mode is not enabled for this Anthropic organization: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.True(t, sawTextDelta, "Expected at least one text_delta event in fast mode stream")

	var hasText bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeText && block.Text != "" {
			hasText = true
			t.Logf("Fast-mode text: %q", block.Text)
		}
	}
	assert.True(t, hasText, "Expected text content in fast-mode response")

	assert.NotEmpty(t, response.StopReason, "StopReason should not be empty")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Fast-mode elapsed: %s", elapsed)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, StopReason: %s", response.Model, response.StopReason)
}

// TestAnthropicResponsesProvider_FastModeSpeedup is an opt-in comparative
// timing check. It runs the same prompt with fast mode on vs off and verifies
// that the fast-mode runs are meaningfully quicker on average, which would not
// be the case if the speed parameter / beta header were not actually being
// sent. Gated behind SIDE_ANTHROPIC_FAST_MODE_BENCH=true (in addition to
// SIDE_INTEGRATION_TEST) so it does not run as part of normal integration test
// sweeps.
func TestAnthropicResponsesProvider_FastModeSpeedup(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}
	if os.Getenv("SIDE_ANTHROPIC_FAST_MODE_BENCH") != "true" {
		t.Skip("Skipping fast-mode speedup benchmark; SIDE_ANTHROPIC_FAST_MODE_BENCH not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel)
	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAny}
	secretManager := requireIntegrationAPIKey(t, llm.AnthropicOAuthSecretName, "ANTHROPIC_API_KEY")

	fastModel := os.Getenv("ANTHROPIC_FAST_MODEL")
	if fastModel == "" {
		fastModel = "claude-opus-4-8"
	}

	// Generation time (output token throughput) dominates the latency
	// difference between fast and normal modes, so we keep input modest while
	// requesting a meaningful amount of output. The values below balance
	// signal-to-noise against keeping the test reasonably quick.
	var promptBuilder strings.Builder
	promptBuilder.WriteString("You are given the following corpus. Read it carefully, then write a long, thorough essay (aim for roughly 1200 words) that synthesizes its themes and examples. Produce only prose, no lists or code blocks. Vary sentence structure and avoid repetition.\n\nCORPUS:\n")
	filler := "Binary search trees are hierarchical data structures in which each node has at most two children, conventionally called the left and right child. The defining invariant is that for any node N, every key in N's left subtree is strictly less than N's key, and every key in N's right subtree is strictly greater. This invariant supports logarithmic-time search, insertion, and deletion when the tree is balanced. In practice, naive insertion order can degrade the structure into a linked list, motivating self-balancing variants such as AVL trees, red-black trees, splay trees, and treaps. "
	for promptBuilder.Len() < 5000 {
		promptBuilder.WriteString(filler)
	}
	prompt := promptBuilder.String()
	const maxTokens = 1500
	const iterations = 2

	run := func(t *testing.T, speed string) time.Duration {
		// Generous per-request deadline: at normal speed, ~1500 output tokens
		// on Opus can comfortably exceed a minute, so a 90s budget would
		// spuriously fail. 5 minutes leaves headroom for transient slowness
		// without letting a true hang sit forever.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		options := Options{
			ModelConfig: common.ModelConfig{
				Provider:  "anthropic",
				Model:     fastModel,
				Speed:     speed,
				MaxTokens: maxTokens,
			},
		}
		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentBlockTypeText, Text: prompt}},
		}}

		eventChan := make(chan Event, 100)
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			for range eventChan {
			}
		}()

		start := time.Now()
		response, err := provider.Stream(ctx, StreamRequest{
			Messages:      messages,
			Options:       options,
			SecretManager: secretManager,
		}, eventChan)
		close(eventChan)
		<-doneCh
		elapsed := time.Since(start)

		if err != nil {
			if isAnthropicTransientError(err) {
				t.Skipf("Skipping due to transient Anthropic API error: %v", err)
			}
			if isAnthropicCredentialError(err) {
				t.Skipf("Skipping due to Anthropic credentials not configured/invalid: %v", err)
			}
			if contains(err.Error(), "does not support the `speed` parameter") {
				t.Skipf("Skipping: model %q does not support speed (set ANTHROPIC_FAST_MODEL to a supported model): %v", fastModel, err)
			}
			t.Fatalf("Stream returned an error (speed=%q): %v", speed, err)
		}
		if response == nil {
			t.Fatalf("Stream returned a nil response (speed=%q)", speed)
		}
		if response.Usage.OutputTokens <= 0 {
			t.Fatalf("expected output tokens > 0 (speed=%q)", speed)
		}
		t.Logf("speed=%q elapsed=%s outputTokens=%d", speed, elapsed, response.Usage.OutputTokens)
		return elapsed
	}

	avg := func(ds []time.Duration) time.Duration {
		var total time.Duration
		for _, d := range ds {
			total += d
		}
		return total / time.Duration(len(ds))
	}

	fmt.Printf("\n=== Fast Mode Speedup Benchmark (%s, %d iterations per mode) ===\n", fastModel, iterations)

	var normalDurations, fastDurations []time.Duration
	// Interleave runs so transient network/load conditions affect both modes
	// roughly equally rather than biasing one set. Log running averages after
	// each pair so partial progress is visible even if a later run fails or
	// the test is interrupted.
	for i := 0; i < iterations; i++ {
		normalDurations = append(normalDurations, run(t, ""))
		fastDurations = append(fastDurations, run(t, "fast"))
		partialNormal := avg(normalDurations)
		partialFast := avg(fastDurations)
		t.Logf("after iteration %d/%d: normal avg=%s, fast avg=%s, speedup=%.2fx",
			i+1, iterations, partialNormal, partialFast, float64(partialNormal)/float64(partialFast))
	}

	normalAvg := avg(normalDurations)
	fastAvg := avg(fastDurations)
	t.Logf("normal avg=%s, fast avg=%s, speedup=%.2fx", normalAvg, fastAvg, float64(normalAvg)/float64(fastAvg))

	// Fast mode is documented to be substantially quicker; require it be
	// noticeably faster on average. A small margin guards against flakes from
	// network jitter while still failing loudly if speed were silently ignored.
	if fastAvg >= normalAvg {
		t.Fatalf("expected fast-mode avg latency (%s) to be lower than normal-mode avg (%s)", fastAvg, normalAvg)
	}
}
func TestAccumulateAnthropicMessageMetadataIgnoresMalformedToolInput(t *testing.T) {
	t.Parallel()

	rawEvents := []string{
		`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-opus-4-8","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_test","name":"get_help_or_input","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"requests\": <parameter name=\"content\">broken"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":9}}`,
	}

	var message anthropic.Message
	for _, rawEvent := range rawEvents {
		var event anthropic.MessageStreamEventUnion
		assert.NoError(t, json.Unmarshal([]byte(rawEvent), &event))
		assert.NoError(t, accumulateAnthropicMessageMetadata(&message, event))
	}

	assert.Equal(t, "msg_test", message.ID)
	assert.Equal(t, int64(12), message.Usage.InputTokens)
	assert.Equal(t, int64(9), message.Usage.OutputTokens)
	assert.Equal(t, anthropic.StopReasonToolUse, message.StopReason)
	assert.Empty(t, message.Content)
}

func anthropicBlockKind(block anthropic.ContentBlockParamUnion) string {
	switch {
	case block.OfText != nil:
		return "text"
	case block.OfThinking != nil:
		return "thinking"
	case block.OfRedactedThinking != nil:
		return "redacted_thinking"
	case block.OfToolUse != nil:
		return "tool_use"
	case block.OfToolResult != nil:
		return "tool_result"
	case block.OfImage != nil:
		return "image"
	default:
		return "unknown"
	}
}

func TestMessagesToAnthropicParams_ReasoningCleanup(t *testing.T) {
	t.Parallel()

	textBlock := ContentBlock{Type: ContentBlockTypeText, Text: "The answer is 44323."}
	toolUseBlock := ContentBlock{
		Type: ContentBlockTypeToolUse,
		ToolUse: &ToolUseBlock{
			Id:        "toolu_1",
			Name:      "get_current_weather",
			Arguments: `{"location":"New York"}`,
		},
	}
	openaiReasoningBlock := ContentBlock{
		Id:   "rs_abc123",
		Type: ContentBlockTypeReasoning,
		Reasoning: &ReasoningBlock{
			Summary:          "Multiplying step by step",
			EncryptedContent: "gAAAAABopenai-encrypted-reasoning",
		},
	}

	testCases := []struct {
		name          string
		content       []ContentBlock
		expectedKinds []string
	}{
		{
			name: "anthropic thinking block is replayed",
			content: []ContentBlock{
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "127 * 349 = ...", Signature: []byte("anthropic-signature")}},
				textBlock,
			},
			expectedKinds: []string{"thinking", "text"},
		},
		{
			name: "anthropic redacted thinking block is replayed",
			content: []ContentBlock{
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{EncryptedContent: "anthropic-opaque-data"}},
				textBlock,
			},
			expectedKinds: []string{"redacted_thinking", "text"},
		},
		{
			name:          "openai reasoning is dropped without losing later blocks",
			content:       []ContentBlock{openaiReasoningBlock, textBlock, toolUseBlock},
			expectedKinds: []string{"text", "tool_use"},
		},
		{
			name: "reasoning missing thinking text or signature is dropped",
			content: []ContentBlock{
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "unsigned thinking"}},
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Summary: "summary only"}},
				{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{}},
				{Type: ContentBlockTypeReasoning},
				textBlock,
			},
			expectedKinds: []string{"text"},
		},
		{
			name:          "assistant message with only foreign reasoning is omitted",
			content:       []ContentBlock{openaiReasoningBlock},
			expectedKinds: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params, err := messagesToAnthropicParams([]Message{{Role: RoleAssistant, Content: tc.content}})
			assert.NoError(t, err)

			if len(tc.expectedKinds) == 0 {
				assert.Empty(t, params)
				return
			}

			if !assert.Len(t, params, 1) {
				return
			}

			var kinds []string
			for _, block := range params[0].Content {
				kinds = append(kinds, anthropicBlockKind(block))
				if block.OfThinking != nil {
					assert.NotEmpty(t, block.OfThinking.Thinking, "thinking blocks must contain thinking")
					assert.NotEmpty(t, block.OfThinking.Signature, "thinking blocks must contain a signature")
				}
				if block.OfRedactedThinking != nil {
					assert.NotEmpty(t, block.OfRedactedThinking.Data, "redacted thinking blocks must contain data")
				}
			}
			assert.Equal(t, tc.expectedKinds, kinds)
		})
	}
}

// Covers switching models mid-conversation, which previously failed with
// "each thinking block must contain thinking" once an OpenAI reasoning turn
// was replayed to Anthropic.
func TestAnthropicProvider_CrossProviderReasoningHandoffIntegration(t *testing.T) {
	t.Parallel()

	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")
	if !hasAnyIntegrationSecret(secretManager, "OPENAI_OAUTH", "OPENAI_API_KEY") {
		t.Skip("Skipping cross-provider handoff test; no OpenAI credentials available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	openaiModel := os.Getenv("OPENAI_REASONING_MODEL")
	if openaiModel == "" {
		openaiModel = "gpt-5.6-luna"
	}
	anthropicModel := os.Getenv("ANTHROPIC_REASONING_MODEL")
	if anthropicModel == "" {
		anthropicModel = "claude-sonnet-4-5"
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "What is 127 * 349? Think through this step by step, showing your work.",
				},
			},
		},
	}

	openaiEvents := make(chan Event, 1000)
	go func() {
		for range openaiEvents {
		}
	}()
	openaiResponse, err := OpenAIResponsesProvider{}.Stream(ctx, StreamRequest{
		Messages: messages,
		Options: Options{
			ModelConfig: common.ModelConfig{
				Provider:        "openai",
				Model:           openaiModel,
				ReasoningEffort: "medium",
				MaxTokens:       2048,
			},
		},
		SecretManager: secretManager,
	}, openaiEvents)
	close(openaiEvents)
	if err != nil {
		t.Skipf("Skipping cross-provider handoff test; OpenAI stream failed: %v", err)
	}

	assistantTurn := openaiResponse.Output
	var openaiReasoning *ReasoningBlock
	var openaiReasoningId string
	for _, block := range assistantTurn.Content {
		if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
			openaiReasoning = block.Reasoning
			openaiReasoningId = block.Id
			break
		}
	}
	if openaiReasoning == nil {
		t.Fatalf("Expected OpenAI reasoning block to replay to Anthropic, got content: %v", assistantTurn.Content)
	}
	t.Logf("OpenAI reasoning block: id=%s textLen=%d summaryLen=%d encryptedContentLen=%d",
		openaiReasoningId, len(openaiReasoning.Text), len(openaiReasoning.Summary), len(openaiReasoning.EncryptedContent))

	handoffMessages := append(append([]Message{}, messages...),
		assistantTurn,
		Message{
			Role:    RoleUser,
			Content: TextContentBlocks("Now double-check that result and reply with just the number."),
		},
	)

	params, err := messagesToAnthropicParams(handoffMessages)
	assert.NoError(t, err)
	for _, param := range params {
		for _, block := range param.Content {
			if block.OfThinking != nil {
				assert.NotEmpty(t, block.OfThinking.Thinking, "thinking blocks must contain thinking")
				assert.NotEmpty(t, block.OfThinking.Signature, "thinking blocks must contain a signature")
			}
			if block.OfRedactedThinking != nil {
				assert.NotEqual(t, openaiReasoning.EncryptedContent, block.OfRedactedThinking.Data,
					"OpenAI encrypted reasoning must not be replayed as Anthropic redacted thinking")
			}
		}
	}

	anthropicEvents := make(chan Event, 1000)
	go func() {
		for range anthropicEvents {
		}
	}()
	anthropicResponse, err := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}.Stream(ctx, StreamRequest{
		Messages: handoffMessages,
		Options: Options{
			ModelConfig: common.ModelConfig{
				Provider:        "anthropic",
				Model:           anthropicModel,
				ReasoningEffort: "low",
				MaxTokens:       2048,
			},
		},
		SecretManager: secretManager,
	}, anthropicEvents)
	close(anthropicEvents)

	if err != nil {
		if isAnthropicTransientError(err) {
			t.Skipf("Skipping cross-provider handoff test due to transient Anthropic API error: %v", err)
		}
		if isAnthropicCredentialError(err) {
			t.Skipf("Skipping cross-provider handoff test due to Anthropic credentials not configured/invalid: %v", err)
		}
		t.Fatalf("Anthropic stream failed after switching from OpenAI: %v", err)
	}

	assert.NotEmpty(t, anthropicResponse.Output.Content)
	assert.Greater(t, anthropicResponse.Usage.InputTokens, 0)
}
