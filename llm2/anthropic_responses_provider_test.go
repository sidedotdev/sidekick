package llm2

import (
	"context"
	"encoding/json"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestAnthropicResponsesProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := AnthropicResponsesProvider{}

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
				Provider: "anthropic",
				Model:    "claude-sonnet-4-5",
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

func TestAnthropicResponsesProvider_Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := AnthropicResponsesProvider{}

	mockTool := &common.Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "anthropic",
				Model:    "",
			},
			Messages: []Message{
				{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeText,
							Text: "First say hi. After that, then look up what the weather is like in New York in celsius. Let me know, then check London too for me.",
						},
					},
				},
			},
			Tools:      []*common.Tool{mockTool},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
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

	if !sawBlockStartedToolUse && !sawTextDelta {
		t.Error("Expected to see at least one block_started event with tool_use or text_delta event")
	}

	t.Logf("Response output content blocks: %d", len(response.Output.Content))

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
