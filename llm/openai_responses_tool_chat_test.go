package llm

import (
	"context"
	"encoding/json"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestOpenaiResponsesChatStream_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	openaiResponsesToolChat := OpenaiResponsesToolChat{}
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{
					Role:    ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    OpenaiResponsesDefaultModel,
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	defer close(deltaChan)
	progressChan := make(chan ProgressInfo)
	defer close(progressChan)
	_, err := openaiResponsesToolChat.ChatStream(ctx, options, deltaChan, progressChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestOpenaiResponsesToolChatIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	chat := OpenaiResponsesToolChat{}

	mockTool := &Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	options := ToolChatOptions{
		Params: ToolChatParams{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Look up what the weather is like in New York in celsius, then describe it to me concisely."},
			},
			Tools:      []*Tool{mockTool},
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

	deltaChan := make(chan ChatMessageDelta)
	var allDeltas []ChatMessageDelta

	go func() {
		for delta := range deltaChan {
			allDeltas = append(allDeltas, delta)
		}
	}()

	progressChan := make(chan ProgressInfo)
	defer close(progressChan)
	response, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
	close(deltaChan)

	if err != nil {
		t.Fatalf("ChatStream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("ChatStream returned a nil response")
	}

	if len(allDeltas) == 0 {
		t.Error("No deltas received")
	}

	t.Logf("Response content: %s", response.Content)

	if len(response.ToolCalls) == 0 {
		t.Fatal("No tool calls in the response")
	}

	toolCall := response.ToolCalls[0]
	if toolCall.Name != "get_current_weather" {
		t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
	}

	t.Logf("Tool call: %+v", toolCall)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

	var args map[string]string
	err = json.Unmarshal([]byte(toolCall.Arguments), &args)
	if err != nil {
		t.Fatalf("Failed to parse tool call arguments: %v", err)
	}

	if !strings.Contains(strings.ToLower(args["location"]), "new york") {
		t.Errorf("Expected location to contain 'New York', got '%s'", args["location"])
	}
	if args["unit"] != "celsius" && args["unit"] != "fahrenheit" {
		t.Errorf("Expected unit 'celsius' or 'fahrenheit', got '%s'", args["unit"])
	}

	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Run("MultiTurn", func(t *testing.T) {
		options.Params.Messages = append(options.Params.Messages, response.ChatMessage)

		for _, tc := range response.ToolCalls {
			var content string
			var argsMap map[string]string
			if err := json.Unmarshal([]byte(tc.Arguments), &argsMap); err == nil {
				if strings.Contains(strings.ToLower(argsMap["location"]), "new york") {
					content = "25"
				} else if strings.Contains(strings.ToLower(argsMap["location"]), "london") {
					content = "18"
				} else {
					content = "20"
				}
			} else {
				content = "20"
			}

			options.Params.Messages = append(options.Params.Messages, ChatMessage{
				Role:       ChatMessageRoleTool,
				Content:    content,
				ToolCallId: tc.Id,
				Name:       tc.Name,
				IsError:    false,
			})
		}

		deltaChan := make(chan ChatMessageDelta)
		var allDeltas []ChatMessageDelta

		go func() {
			for delta := range deltaChan {
				allDeltas = append(allDeltas, delta)
			}
		}()

		progressChan := make(chan ProgressInfo)
		defer close(progressChan)

		response, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
		close(deltaChan)

		if err != nil {
			t.Fatalf("ChatStream returned an error: %v", err)
		}

		if response == nil {
			t.Fatal("ChatStream returned a nil response")
		}

		if len(allDeltas) == 0 {
			t.Error("No deltas received")
		}

		t.Logf("Response content: %s", response.Content)
		t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

		if response.Content == "" {
			t.Error("Response content is empty after providing tool results")
		}

		assert.NotNil(t, response.Usage, "Usage field should not be nil on multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})
}
