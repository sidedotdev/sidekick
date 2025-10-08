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

func TestOpenaiResponsesToolChatIntegration_Basic(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	chat := OpenaiResponsesToolChat{}

	options := ToolChatOptions{
		Params: ToolChatParams{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Say hello in one sentence."},
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

	if response.Content == "" {
		t.Error("Response content is empty")
	}

	if response.Usage.InputTokens == 0 && response.Usage.OutputTokens == 0 {
		t.Log("Warning: Usage tokens are zero (may not be provided by model)")
	}

	t.Logf("Response content: %s", response.Content)
	t.Logf("Usage: %+v", response.Usage)
	t.Logf("Deltas received: %d", len(allDeltas))
}

func TestOpenaiResponsesToolChatIntegration_Tools(t *testing.T) {
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
				Model:    "gpt-4o-mini",
			},
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "First say hi. After that, then look up what the weather is like in New York"},
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

	if len(response.ToolCalls) == 0 {
		t.Fatal("No tool calls in the response")
	}

	toolCall := response.ToolCalls[0]
	if toolCall.Name != "get_current_weather" {
		t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
	}

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

	t.Logf("Response content: %s", response.Content)
	t.Logf("Tool call: %+v", toolCall)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

	if response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0 {
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")
	}
}

func TestOpenaiResponsesToolChatIntegration_MultiTurn(t *testing.T) {
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
				Model:    "gpt-4o-mini",
			},
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "First say hi. After that, then look up what the weather is like in New York"},
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

	if len(response.ToolCalls) == 0 {
		t.Fatal("No tool calls in the first response")
	}

	toolCall := response.ToolCalls[0]

	options.Params.Messages = append(options.Params.Messages, response.ChatMessage)
	options.Params.Messages = append(options.Params.Messages, ChatMessage{
		Role:       ChatMessageRoleTool,
		Content:    "Warm and Sunny",
		ToolCallId: toolCall.Id,
		Name:       toolCall.Name,
		IsError:    false,
	})
	options.Params.Messages = append(options.Params.Messages, ChatMessage{
		Role:    ChatMessageRoleUser,
		Content: "How about London?",
	})

	deltaChan = make(chan ChatMessageDelta)
	allDeltas = nil

	go func() {
		for delta := range deltaChan {
			allDeltas = append(allDeltas, delta)
		}
	}()

	progressChan = make(chan ProgressInfo)
	defer close(progressChan)
	response, err = chat.ChatStream(ctx, options, deltaChan, progressChan)
	close(deltaChan)

	if err != nil {
		t.Fatalf("ChatStream returned an error on multi-turn: %v", err)
	}

	if response == nil {
		t.Fatal("ChatStream returned a nil response on multi-turn")
	}

	if len(allDeltas) == 0 {
		t.Error("No deltas received on multi-turn")
	}

	if len(response.ToolCalls) == 0 {
		t.Fatal("No tool calls in the multi-turn response")
	}

	toolCall = response.ToolCalls[0]
	if toolCall.Name != "get_current_weather" {
		t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
	}

	var args map[string]string
	err = json.Unmarshal([]byte(toolCall.Arguments), &args)
	if err != nil {
		t.Fatalf("Failed to parse tool call arguments on multi-turn: %v", err)
	}

	if !strings.Contains(strings.ToLower(args["location"]), "london") {
		t.Errorf("Expected location to contain 'london', got '%s'", args["location"])
	}
	if args["unit"] != "celsius" && args["unit"] != "fahrenheit" {
		t.Errorf("Expected unit 'celsius' or 'fahrenheit', got '%s'", args["unit"])
	}

	t.Logf("Response content (multi-turn): %s", response.Content)
	t.Logf("Tool call (multi-turn): %+v", toolCall)
	t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

	if response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0 {
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	}
}
