package llm

import (
	"context"
	"fmt"
	"sidekick/secret_manager"

	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
	"github.com/sashabaranov/go-openai"
)

const defaultTemperature float32 = 0.1

type ToolChatter interface {
	ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error)
}

type ChatControlParams struct {
	Temperature *float32         `json:"temperature"`
	Model       string           `json:"model"`
	Provider    ToolChatProvider `json:"provider"`
}

// chat params for LLMs that support automatic tool selection (provided multiple
// tools, these LLMs can decide when it is appropriate to use a tool and use the
// tool via a tool call
type ToolChatParams struct {
	Messages          []ChatMessage    `json:"messages"`
	Tools             []*Tool          `json:"tools"`
	ToolChoice        ToolChoice       `json:"toolChoice"`
	ParallelToolCalls *bool            `json:"parallelToolCalls"`
	Temperature       *float32         `json:"temperature"`
	Model             string           `json:"model"`
	Provider          ToolChatProvider `json:"provider"`
}

func PromptToToolChatParams(prompt string, controlParams ChatControlParams) ToolChatParams {
	return ToolChatParams{
		Messages: []ChatMessage{
			{
				Content: prompt,
				Role:    ChatMessageRoleUser,
			},
		},
		Temperature: controlParams.Temperature,
		Model:       controlParams.Model,
		Provider:    controlParams.Provider,
	}
}

type ChatProvider string

const (
	UnspecifiedChatProvider ChatProvider = ""
	OpenaiChatProvider      ChatProvider = "openai"
	AnthropicChatProvider   ChatProvider = "anthropic"
)

type ToolChatProvider string

const (
	UnspecifiedToolChatProvider ToolChatProvider = ""
	OpenaiToolChatProvider      ToolChatProvider = "openai"
	AnthropicToolChatProvider   ToolChatProvider = "anthropic"
)

var SmallModels = map[ToolChatProvider]string{
	OpenaiToolChatProvider:    "gpt-4o-mini",
	AnthropicToolChatProvider: string(anthropic.Claude3Haiku), // NOTE: 3.5 Haiku is much more expensive
}

func (provider ToolChatProvider) SmallModel() string {
	// missing will be empty string, i.e. the internal/built-in default model
	// for the provider integration implementation
	return SmallModels[provider]
}

var LongContextLargeModels = map[ToolChatProvider]string{
	OpenaiToolChatProvider:    openai.GPT4Turbo20240409,
	AnthropicToolChatProvider: string(anthropic.Claude3Opus),
}

func (provider ToolChatProvider) LongContextLargeModel() string {
	return LongContextLargeModels[provider]
}

func StringToToolChatProvider(provider string) (ToolChatProvider, error) {
	switch provider {
	case string(OpenaiToolChatProvider):
		return OpenaiToolChatProvider, nil
	case string(AnthropicToolChatProvider):
		return AnthropicToolChatProvider, nil
	case string(UnspecifiedToolChatProvider):
		return UnspecifiedToolChatProvider, nil
	default:
		return UnspecifiedToolChatProvider, fmt.Errorf("unknown provider: %s", provider)
	}
}

type ToolChatOptions struct {
	Params  ToolChatParams                        `json:"params"`
	Secrets secret_manager.SecretManagerContainer `json:"secrets"`
}

func (options ToolChatOptions) ActionParams() map[string]any {
	return map[string]any{
		"messages":          options.Params.Messages,
		"tools":             options.Params.Tools,
		"toolChoice":        options.Params.ToolChoice,
		"model":             options.Params.Model,
		"provider":          options.Params.Provider,
		"temperature":       options.Params.Temperature,
		"parallelToolCalls": options.Params.ParallelToolCalls,
	}
}
