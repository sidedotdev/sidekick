package llm

import (
	"context"
	"encoding/json"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
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
			Role:    ChatMessageRoleUser,
			Content: "Hello",
		},
		{
			Role:    ChatMessageRoleAssistant,
			Content: "Hi there!",
			ToolCalls: []ToolCall{
				{
					Name:      "search",
					Arguments: `{"query": "test"}`,
				},
			},
		},
	}

	result, err := anthropicFromChatMessages(input)
	assert.NoError(t, err)
	assert.Len(t, result, 2)

	assert.Equal(t, anthropic.MessageParamRoleUser, result[0].Role.Value)
	assert.Len(t, result[0].Content.Value, 1)
	anthropicTextBlock := result[0].Content.Value[0].(anthropic.TextBlockParam)
	assert.Equal(t, anthropic.TextBlockParamTypeText, anthropicTextBlock.Type.Value)
	assert.Equal(t, "Hello", anthropicTextBlock.Text.Value)

	assert.Equal(t, anthropic.MessageParamRoleAssistant, result[1].Role.Value)
	assert.Len(t, result[1].Content.Value, 2)
	anthropicTextBlock2 := result[1].Content.Value[0].(anthropic.TextBlockParam)
	assert.Equal(t, anthropic.TextBlockParamTypeText, anthropicTextBlock2.Type.Value)
	assert.Equal(t, "Hi there!", anthropicTextBlock2.Text.Value)

	anthropicToolUseBlock := result[1].Content.Value[1].(anthropic.ToolUseBlockParam)
	assert.Equal(t, anthropic.ToolUseBlockParamTypeToolUse, anthropicToolUseBlock.Type.Value)
	assert.Equal(t, "search", anthropicToolUseBlock.Name.Value)
	assert.Equal(t, `{"query": "test"}`, string(anthropicToolUseBlock.Input.Value.(json.RawMessage)))
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
