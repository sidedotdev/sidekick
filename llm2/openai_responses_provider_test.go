package llm2

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"sidekick/utils"
	"strings"
	"sync"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func newTestChatHistoryWithMessages(messages []Message) *ChatHistoryContainer {
	chatHistory := NewLlm2ChatHistory("test-flow", "test-workspace")
	chatHistory.SetMessages(messages)
	return &ChatHistoryContainer{History: chatHistory}
}

type getCurrentWeather struct {
	Location string `json:"location"`
	Unit     string `json:"unit" jsonschema:"enum=celsius,fahrenheit"`
}

func TestOpenAIResponsesProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := OpenAIResponsesProvider{}

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
				Provider: "openai",
				Model:    "gpt-5-codex",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, options, eventChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestOpenAIResponsesProvider_Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := OpenAIResponsesProvider{}

	fmt.Println("\n=== OpenAI Responses Provider Integration Test ===")

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
					Text: "First say hi. After that, then look up what the weather is like in New York in celsius, then describe it in words.",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
			Temperature: utils.Ptr(float32(0)),
			Tools:       []*common.Tool{mockTool},
			ToolChoice:  common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawBlockStartedToolUse bool
	var sawTextDelta bool
	eventIdx := 0

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			debugPrintOpenAIEvent(eventIdx, event)
			eventIdx++
			if event.Type == EventBlockStarted && event.ContentBlock.Type == ContentBlockTypeToolUse {
				sawBlockStartedToolUse = true
			}
			if event.Type == EventTextDelta {
				sawTextDelta = true
			}
		}
	}()

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	if len(allEvents) == 0 {
		t.Error("No events received")
	}

	if !sawBlockStartedToolUse {
		t.Error("Expected to see at least one block_started event with tool_use")
	}

	if !sawTextDelta {
		t.Error("Expected to see at least one text_delta event")
	}

	t.Logf("Response output content blocks: %d", len(response.Output.Content))
	debugPrintAllContentBlocks(response.Output.Content)

	var foundToolUse bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse {
			foundToolUse = true
			if block.ToolUse.Name == "get_current_weather" {
				t.Logf("Found tool_use block: %+v", block.ToolUse)
				break
			}
		}
	}

	if !foundToolUse {
		t.Error("Expected response.Output.Content to include a tool_use block with Name 'get_current_weather'")
	}

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

		options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)
		response, err := provider.Stream(ctx, options, eventChan)
		close(eventChan)

		if err != nil {
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

		var hasTextContent bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				hasTextContent = true
				break
			} else {
				t.Logf("Output Block: %s", utils.PanicJSON(block))
			}
		}

		if !hasTextContent {
			t.Error("Response content is empty after providing tool results")
		}

		assert.NotNil(t, response.Usage, "Usage field should not be nil on multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})
}

func TestOpenAIResponsesProvider_ReasoningEncryptedContinuation(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := OpenAIResponsesProvider{}

	fmt.Println("\n=== OpenAI Responses Reasoning Test ===")

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "What is 127 * 349? Think step by step and show your work.",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider:        "openai",
				Model:           "gpt-5.2",
				ReasoningEffort: "low",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawSummaryTextDelta bool
	eventIdx := 0

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			debugPrintOpenAIEvent(eventIdx, event)
			eventIdx++
			if event.Type == EventSummaryTextDelta {
				sawSummaryTextDelta = true
			}
		}
	}()

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	if !sawSummaryTextDelta {
		t.Logf("Note: No summary_text_delta events received (may be expected for simple prompts)")
	}

	t.Logf("Response output content blocks: %d", len(response.Output.Content))
	debugPrintAllContentBlocks(response.Output.Content)

	var foundReasoning bool
	var encryptedContent string
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
			foundReasoning = true
			encryptedContent = block.Reasoning.EncryptedContent
			t.Logf("Found reasoning block with EncryptedContent length: %d", len(encryptedContent))
			break
		}
	}

	if !foundReasoning {
		t.Error("Expected response.Output.Content to include a reasoning block")
	}

	if encryptedContent == "" {
		t.Error("Expected reasoning block to have non-empty EncryptedContent")
	}

	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)

	t.Run("MultiTurnEncryptedReasoning", func(t *testing.T) {
		messages = append(messages, response.Output)

		messages = append(messages, Message{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "How are you?",
				},
			},
		})

		eventChan := make(chan Event, 100)
		go func() {
			for range eventChan {
			}
		}()

		options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)
		response, err := provider.Stream(ctx, options, eventChan)
		close(eventChan)

		if err != nil {
			t.Fatalf("Stream returned an error on multi-turn: %v", err)
		}

		if response == nil {
			t.Fatal("Stream returned a nil response on multi-turn")
		}

		t.Logf("Response output content blocks (multi-turn): %d", len(response.Output.Content))
		t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

		var hasTextContent bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				hasTextContent = true
				break
			}
		}

		if !hasTextContent {
			t.Error("Response content is empty after providing encrypted reasoning continuation")
		}

		assert.NotNil(t, response.Usage, "Usage field should not be nil on multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})
}

func TestAccumulateOpenaiEventsToMessage_BlockDone(t *testing.T) {
	events := []Event{
		{
			Type:  EventBlockStarted,
			Index: 0,
			ContentBlock: &ContentBlock{
				Type: ContentBlockTypeReasoning,
				Reasoning: &ReasoningBlock{
					Text:    "initial text",
					Summary: "initial summary",
				},
			},
		},
		{
			Type:  EventBlockDone,
			Index: 0,
			ContentBlock: &ContentBlock{
				Type: ContentBlockTypeReasoning,
				Reasoning: &ReasoningBlock{
					Text:             "final text",
					EncryptedContent: "encrypted_final_value",
				},
			},
		},
	}

	message := accumulateOpenaiEventsToMessage(events)

	assert.Equal(t, RoleAssistant, message.Role)
	assert.Len(t, message.Content, 1)
	assert.Equal(t, ContentBlockTypeReasoning, message.Content[0].Type)
	assert.NotNil(t, message.Content[0].Reasoning)
	assert.Equal(t, "final text", message.Content[0].Reasoning.Text)
	assert.Equal(t, "initial summary", message.Content[0].Reasoning.Summary)
	assert.Equal(t, "encrypted_final_value", message.Content[0].Reasoning.EncryptedContent)
}

func debugPrintOpenAIEvent(idx int, event Event) {
	switch event.Type {
	case EventBlockStarted:
		if event.ContentBlock != nil {
			block := event.ContentBlock
			switch block.Type {
			case ContentBlockTypeText:
				fmt.Printf("Event[%d]: type=block_started block_type=text id=%s\n", idx, block.Id)
			case ContentBlockTypeToolUse:
				if block.ToolUse != nil {
					fmt.Printf("Event[%d]: type=block_started block_type=tool_use id=%s name=%s\n", idx, block.ToolUse.Id, block.ToolUse.Name)
				}
			case ContentBlockTypeReasoning:
				if block.Reasoning != nil {
					fmt.Printf("Event[%d]: type=block_started block_type=reasoning text_len=%d summary_len=%d encrypted_len=%d\n",
						idx, len(block.Reasoning.Text), len(block.Reasoning.Summary), len(block.Reasoning.EncryptedContent))
				} else {
					fmt.Printf("Event[%d]: type=block_started block_type=reasoning (no reasoning block)\n", idx)
				}
			case ContentBlockTypeRefusal:
				fmt.Printf("Event[%d]: type=block_started block_type=refusal\n", idx)
			default:
				fmt.Printf("Event[%d]: type=block_started block_type=%s\n", idx, block.Type)
			}
		}
	case EventTextDelta:
		deltaPreview := event.Delta
		if len(deltaPreview) > 50 {
			deltaPreview = deltaPreview[:50] + "..."
		}
		fmt.Printf("Event[%d]: type=text_delta index=%d delta=%q\n", idx, event.Index, deltaPreview)
	case EventSummaryTextDelta:
		deltaPreview := event.Delta
		if len(deltaPreview) > 50 {
			deltaPreview = deltaPreview[:50] + "..."
		}
		fmt.Printf("Event[%d]: type=summary_text_delta index=%d delta=%q\n", idx, event.Index, deltaPreview)
	case EventSignatureDelta:
		fmt.Printf("Event[%d]: type=signature_delta index=%d len=%d\n", idx, event.Index, len(event.Delta))
	case EventBlockDone:
		if event.ContentBlock != nil {
			block := event.ContentBlock
			switch block.Type {
			case ContentBlockTypeReasoning:
				if block.Reasoning != nil {
					fmt.Printf("Event[%d]: type=block_done block_type=reasoning text_len=%d summary_len=%d encrypted_len=%d\n",
						idx, len(block.Reasoning.Text), len(block.Reasoning.Summary), len(block.Reasoning.EncryptedContent))
				} else {
					fmt.Printf("Event[%d]: type=block_done block_type=reasoning (no reasoning block)\n", idx)
				}
			default:
				fmt.Printf("Event[%d]: type=block_done block_type=%s\n", idx, block.Type)
			}
		} else {
			fmt.Printf("Event[%d]: type=block_done index=%d\n", idx, event.Index)
		}
	default:
		fmt.Printf("Event[%d]: type=%s index=%d\n", idx, event.Type, event.Index)
	}
}

func debugPrintAllContentBlocks(blocks []ContentBlock) {
	fmt.Printf("\n=== All Content Blocks (total: %d) ===\n", len(blocks))
	for i, block := range blocks {
		switch block.Type {
		case ContentBlockTypeText:
			textPreview := block.Text
			if len(textPreview) > 100 {
				textPreview = textPreview[:100] + "..."
			}
			fmt.Printf("Block[%d] Type=text\n  Text=%q\n", i, textPreview)
		case ContentBlockTypeToolUse:
			if block.ToolUse != nil {
				fmt.Printf("Block[%d] Type=tool_use\n  ToolUse: ID=%s Name=%s ArgsLen=%d\n",
					i, block.ToolUse.Id, block.ToolUse.Name, len(block.ToolUse.Arguments))
			}
		case ContentBlockTypeReasoning:
			if block.Reasoning != nil {
				textPreview := block.Reasoning.Text
				if len(textPreview) > 100 {
					textPreview = textPreview[:100] + "..."
				}
				summaryPreview := block.Reasoning.Summary
				if len(summaryPreview) > 100 {
					summaryPreview = summaryPreview[:100] + "..."
				}
				fmt.Printf("Block[%d] Type=reasoning\n  ReasoningText=%q\n  ReasoningTextLen=%d ReasoningSummary=%q SummaryLen=%d EncryptedLen=%d\n",
					i, textPreview, len(block.Reasoning.Text), summaryPreview, len(block.Reasoning.Summary), len(block.Reasoning.EncryptedContent))
			}
		case ContentBlockTypeRefusal:
			if block.Refusal != nil {
				fmt.Printf("Block[%d] Type=refusal\n  Reason=%s\n", i, block.Refusal.Reason)
			}
		default:
			fmt.Printf("Block[%d] Type=%s\n", i, block.Type)
		}
	}
}

func TestOpenAIResponsesProvider_ToolResultImageIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	ctx := context.Background()
	provider := OpenAIResponsesProvider{}

	expectedText, dataURL := GenerateVisionTestImage(6)
	t.Logf("Generated vision test image with text: %q", expectedText)

	toolCallId := "tool_call_img_001"
	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Please use the read_image tool to read the image at path 'test.png' and tell me the exact text in it.",
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
						Text:       "Here is the image content:",
						Content: []ContentBlock{
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

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    defaultModel,
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
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

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

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)
	wg.Wait()

	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") {
			t.Skipf("Skipping test due to OpenAI API rate limit: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	assert.NotNil(t, response)
	responseText := strings.TrimSpace(fullText.String())
	t.Logf("Model response: %q", responseText)
	assert.True(t, VisionTestFuzzyMatch(expectedText, responseText),
		"Expected model to read %q from the image, got %q", expectedText, responseText)
}
