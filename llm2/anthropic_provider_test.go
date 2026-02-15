package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"sync"
	"testing"

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
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-5",
			},
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
	provider := AnthropicProvider{}

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

	secretManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "",
			},
			Tools:      []*common.Tool{mockTool},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
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
		if contains(err.Error(), "overloaded_error") || contains(err.Error(), "Overloaded") {
			t.Skipf("Skipping test due to Anthropic API being overloaded: %v", err)
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

	t.Run("MultiTurn", func(t *testing.T) {
		messages = append(messages, response.Output)

		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeToolResult,
							ToolResult: &ToolResultBlock{
								ToolCallId: block.ToolUse.Id,
								Text:       "25",
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
			if contains(err.Error(), "overloaded_error") || contains(err.Error(), "Overloaded") {
				t.Skipf("Skipping multi-turn test due to Anthropic API being overloaded: %v", err)
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

	t.Run("Reasoning", func(t *testing.T) {
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
			Params: Params{
				ModelConfig: common.ModelConfig{
					Provider:        "anthropic",
					Model:           reasoningModel,
					ReasoningEffort: "low",
				},
			},
		}

		eventChan := make(chan Event, 100)
		var allEvents []Event

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
				case EventSignatureDelta:
					fmt.Printf("Event[%d]: type=signature_delta len=%d\n", event.Index, len(event.Signature))
				default:
					fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
				}
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
			if contains(err.Error(), "overloaded_error") || contains(err.Error(), "Overloaded") {
				t.Skipf("Skipping reasoning test due to Anthropic API being overloaded: %v", err)
			}
			t.Fatalf("Stream returned an error: %v", err)
		}

		if response == nil {
			t.Fatal("Stream returned a nil response")
		}

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
				fmt.Printf("  Text=%q TextLen=%d\n", textPreview, len(block.Text))
			case ContentBlockTypeReasoning:
				if block.Reasoning != nil {
					textPreview := block.Reasoning.Text
					if len(textPreview) > 100 {
						textPreview = textPreview[:100] + "..."
					}
					fmt.Printf("  ReasoningText=%q\n", textPreview)
					fmt.Printf("  ReasoningTextLen=%d SummaryLen=%d SignatureLen=%d\n", len(block.Reasoning.Text), len(block.Reasoning.Summary), len(block.Reasoning.Signature))
				}
			}
		}

		// Check for reasoning content
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
	})
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
							Text:       "result text",
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
			name: "reasoning block with cache control",
			message: Message{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text:    "Let me think about this...",
							Summary: "Thinking",
						},
						CacheControl: "ephemeral",
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
			assert.Contains(t, jsonStr, `"cache_control":{"type":"ephemeral"}`,
				"Expected cache_control to be present in JSON output for %s", tc.name)
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
	provider := AnthropicProvider{}

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
					Text: "What text is written in this image? The text consists only of uppercase ASCII letters (A-Z, no O or I) and digits (2-9). Reply with ONLY the exact text, nothing else.",
				},
			},
		},
	}

	secretManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-5-20250929",
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
		if contains(err.Error(), "overloaded_error") || contains(err.Error(), "Overloaded") || contains(err.Error(), "rate_limit") {
			t.Skipf("Skipping test due to transient Anthropic API error: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	assert.NotNil(t, response)
	responseText := strings.TrimSpace(fullText.String())
	t.Logf("Model response: %q", responseText)
	assert.True(t, VisionTestFuzzyMatch(expectedText, responseText),
		"Expected model to read %q from the image, got %q", expectedText, responseText)
}
