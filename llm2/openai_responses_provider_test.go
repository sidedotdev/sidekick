package llm2

import (
	"context"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

type getCurrentWeather struct {
	Location string `json:"location"`
	Unit     string `json:"unit" jsonschema:"enum=celsius,fahrenheit"`
}

func TestOpenAIResponsesProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := OpenAIResponsesProvider{}

	options := Options{
		Params: Params{
			Messages: []Message{
				{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeText,
							Text: "Hello",
						},
					},
				},
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-5-codex",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

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

	mockTool := &common.Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
			Messages: []Message{
				{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeText,
							Text: "First say hi. After that, then look up what the weather is like in New York in celsius, then describe it in words.",
						},
					},
				},
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

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			if event.Type == EventBlockStarted && event.ContentBlock.Type == ContentBlockTypeToolUse {
				sawBlockStartedToolUse = true
			}
			if event.Type == EventTextDelta {
				sawTextDelta = true
			}
		}
	}()

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
		options.Params.Messages = append(options.Params.Messages, response.Output)

		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				options.Params.Messages = append(options.Params.Messages, Message{
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

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider:        "openai",
				Model:           "gpt-5-nano",
				ReasoningEffort: "minimal",
			},
			Messages: []Message{
				{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeText,
							Text: "Hi",
						},
					},
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

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawSummaryTextDelta bool

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			if event.Type == EventSummaryTextDelta {
				sawSummaryTextDelta = true
			}
		}
	}()

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
		options.Params.Messages = append(options.Params.Messages, response.Output)

		options.Params.Messages = append(options.Params.Messages, Message{
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
