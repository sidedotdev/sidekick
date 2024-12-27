package llm

import (
	"context"

	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
)

const AnthropicDefaultModel = string(anthropic.Claude35Sonnet)
const AnthropicDefaultLongContextModel = string(anthropic.Claude35Sonnet)
const AnthropicApiKeySecretName = "ANTHROPIC_API_KEY"

type AnthropicToolChat struct{}

func (AnthropicToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error) {
	panic("implement me")
}
