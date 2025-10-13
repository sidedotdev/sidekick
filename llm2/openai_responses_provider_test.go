package llm2

import (
	"context"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
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

	if response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0 {
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 when usage is provided")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 when usage is provided")
	}

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)
}
