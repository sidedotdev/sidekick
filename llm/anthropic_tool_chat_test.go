package llm

import (
	"context"
	"encoding/json"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestAnthropicChatStream_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	anthropicToolChat := AnthropicToolChat{}
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{
					Role:    ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			Model:    AnthropicDefaultModel,
			Provider: ToolChatProvider(common.AnthropicChatProvider),
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	defer close(deltaChan)
	_, err := anthropicToolChat.ChatStream(ctx, options, deltaChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestAnthropicFromChatMessages(t *testing.T) {
	input := []ChatMessage{
		{
			Role:    ChatMessageRoleSystem,
			Content: "System message",
		},
		{
			Role:    ChatMessageRoleUser,
			Content: "Hello",
		},
		{
			Role:    ChatMessageRoleAssistant,
			Content: "Hi there!",
			ToolCalls: []ToolCall{
				{
					Id:        "tool_123",
					Name:      "search",
					Arguments: `{"query": "test"}`,
				},
			},
		},
		{
			Role:       ChatMessageRoleTool,
			ToolCallId: "tool_123",
			Name:       "search",
			Content:    "Not found: test",
			IsError:    false,
		},
	}

	result, err := anthropicFromChatMessages(input)
	assert.NoError(t, err)
	assert.Len(t, result, 4)

	assert.Equal(t, anthropic.MessageParamRoleUser, result[0].Role.Value)
	assert.Len(t, result[0].Content.Value, 1)
	anthropicTextBlock := result[0].Content.Value[0].(anthropic.TextBlockParam)
	assert.Equal(t, anthropic.TextBlockParamTypeText, anthropicTextBlock.Type.Value)
	assert.Equal(t, "System message", anthropicTextBlock.Text.Value)

	assert.Equal(t, anthropic.MessageParamRoleUser, result[1].Role.Value)
	assert.Len(t, result[1].Content.Value, 1)
	anthropicTextBlock2 := result[1].Content.Value[0].(anthropic.TextBlockParam)
	assert.Equal(t, anthropic.TextBlockParamTypeText, anthropicTextBlock2.Type.Value)
	assert.Equal(t, "Hello", anthropicTextBlock2.Text.Value)

	assert.Equal(t, anthropic.MessageParamRoleAssistant, result[2].Role.Value)
	assert.Len(t, result[2].Content.Value, 2)
	anthropicTextBlock3 := result[2].Content.Value[0].(anthropic.TextBlockParam)
	assert.Equal(t, anthropic.TextBlockParamTypeText, anthropicTextBlock3.Type.Value)
	assert.Equal(t, "Hi there!", anthropicTextBlock3.Text.Value)

	anthropicToolUseBlock := result[2].Content.Value[1].(anthropic.ToolUseBlockParam)
	assert.Equal(t, anthropic.ToolUseBlockParamTypeToolUse, anthropicToolUseBlock.Type.Value)
	assert.Equal(t, "search", anthropicToolUseBlock.Name.Value)
	assert.Equal(t, `{"query": "test"}`, string(anthropicToolUseBlock.Input.Value.(json.RawMessage)))

	anthropicToolResultBlock := result[3].Content.Value[0].(anthropic.ToolResultBlockParam)
	assert.Equal(t, anthropic.ToolResultBlockParamTypeToolResult, anthropicToolResultBlock.Type.Value)
	assert.Equal(t, "Not found: test", anthropicToolResultBlock.Content.Value[0].(anthropic.TextBlockParam).Text.Value)
	assert.Equal(t, "tool_123", anthropicToolResultBlock.ToolUseID.Value)
	assert.Equal(t, false, anthropicToolResultBlock.IsError.Value)
}

func TestAnthropicToChatMessageResponse(t *testing.T) {
	input := &anthropic.Message{
		ID:   "msg_123",
		Role: anthropic.MessageRoleAssistant,
		Content: []anthropic.ContentBlock{
			{
				Type: anthropic.ContentBlockTypeText,
				Text: "Hello, how can I help you?",
			},
			{
				Type:  anthropic.ContentBlockTypeToolUse,
				Name:  "search",
				Input: json.RawMessage(`{"query": "test"}`),
			},
		},
		Model:      "claude-3-sonnet-20240229",
		StopReason: anthropic.MessageStopReasonEndTurn,
		Usage: anthropic.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	result, err := anthropicToChatMessageResponse(*input)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	assert.Equal(t, "msg_123", result.Id)
	assert.Equal(t, ChatMessageRoleAssistant, result.Role)
	assert.Equal(t, "Hello, how can I help you?", result.Content)
	assert.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "search", result.ToolCalls[0].Name)
	assert.Equal(t, `{"query": "test"}`, string(result.ToolCalls[0].Arguments))
	assert.Equal(t, "claude-3-sonnet-20240229", result.Model)
	assert.Equal(t, string(anthropic.MessageStopReasonEndTurn), result.StopReason)
	assert.Equal(t, Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}, result.Usage)
	assert.Equal(t, common.AnthropicChatProvider, result.Provider)
}

type getCurrentWeather struct {
	Location string `json:"location"`
	Unit     string `json:"unit" jsonschema:"enum=celsius,fahrenheit"`
}

func TestAnthropicToolChatIntegration(t *testing.T) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)

	if os.Getenv("SIDE_INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	ctx := context.Background()
	chat := AnthropicToolChat{}

	// Mock tool for testing
	mockTool := &Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&getCurrentWeather{}),
	}

	options := ToolChatOptions{
		Params: ToolChatParams{
			Model: anthropic.ModelClaude_3_Haiku_20240307, // cheapest model for integration testing
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "First say hi. After thatn, then look up what the weather in New York"},
			},
			Tools: []*Tool{mockTool},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.KeyringSecretManager{},
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	var allDeltas []ChatMessageDelta

	go func() {
		for delta := range deltaChan {
			allDeltas = append(allDeltas, delta)
		}
	}()

	response, err := chat.ChatStream(ctx, options, deltaChan)
	close(deltaChan)

	if err != nil {
		t.Fatalf("ChatStream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("ChatStream returned a nil response")
	}

	// Check that we received deltas
	if len(allDeltas) == 0 {
		t.Error("No deltas received")
	}

	// Check that the response contains content
	if response.Content == "" {
		t.Error("Response content is empty")
	}

	// Check that the response includes a tool call
	if len(response.ToolCalls) == 0 {
		t.Error("No tool calls in the response")
	}

	// Verify tool call
	toolCall := response.ToolCalls[0]
	if toolCall.Name != "get_current_weather" {
		t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
	}

	// Parse tool call arguments
	var args map[string]string
	err = json.Unmarshal([]byte(toolCall.Arguments), &args)
	if err != nil {
		t.Fatalf("Failed to parse tool call arguments: %v", err)
	}

	// Check tool call arguments
	if !strings.Contains(strings.ToLower(args["location"]), "new york") {
		t.Errorf("Expected location to contain 'New York', got '%s'", args["location"])
	}
	if args["unit"] != "celsius" && args["unit"] != "fahrenheit" {
		t.Errorf("Expected unit 'celsius' or 'fahrenheit', got '%s'", args["unit"])
	}

	t.Logf("Response content: %s", response.Content)
	t.Logf("Tool call: %+v", toolCall)
}
