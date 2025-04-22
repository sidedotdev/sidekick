package llm

import (
	"context"
	"sidekick/common"

	"sidekick/secret_manager"
)

const defaultTemperature float32 = 0.1

type ToolChatter interface {
	ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error)
}

type ChatControlParams struct {
	Temperature *float32 `json:"temperature"`
	common.ModelConfig
}

// chat params for LLMs that support automatic tool selection (provided multiple
// tools, these LLMs can decide when it is appropriate to use a tool and use the
// tool via a tool call
type ToolChatParams struct {
	Messages          []ChatMessage `json:"messages"`
	Tools             []*Tool       `json:"tools"`
	ToolChoice        ToolChoice    `json:"toolChoice"`
	ParallelToolCalls *bool         `json:"parallelToolCalls"`
	Temperature       *float32      `json:"temperature"`
	common.ModelConfig
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
		ModelConfig: controlParams.ModelConfig,
	}
}

type ToolChatProviderType = common.ToolChatProviderType

const (
	UnspecifiedToolChatProviderType      ToolChatProviderType = ToolChatProviderType(common.UnspecifiedChatProvider)
	OpenaiToolChatProviderType           ToolChatProviderType = ToolChatProviderType(common.OpenaiChatProvider)
	AnthropicToolChatProviderType        ToolChatProviderType = ToolChatProviderType(common.AnthropicChatProvider)
	OpenaiCompatibleToolChatProviderType ToolChatProviderType = ToolChatProviderType(common.OpenaiCompatibleChatProvider)
	GoogleToolChatProviderType           ToolChatProviderType = ToolChatProviderType(common.GoogleChatProvider)
)

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
