package common

import (
	"fmt"
	"reflect"

	"github.com/ehsanul/anthropic-go/v3/pkg/anthropic"
	"github.com/invopop/jsonschema"
	"github.com/sashabaranov/go-openai"
)

type ChatMessage struct {
	Role      ChatMessageRole `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []ToolCall      `json:"toolCalls"`

	/* for tool call responses */
	Name       string `json:"name"`
	ToolCallId string `json:"toolCallId"`
	IsError    bool   `json:"isError"`
}

type ChatMessageRole string

const (
	ChatMessageRoleUser      ChatMessageRole = "user"
	ChatMessageRoleAssistant ChatMessageRole = "assistant"
	ChatMessageRoleSystem    ChatMessageRole = "system"
	ChatMessageRoleTool      ChatMessageRole = "tool"
)

// represents a message received from a chat provider, i.e. including additional
// metadata around the execution of the chat inference
type ChatMessageResponse struct {
	ChatMessage
	Id           string       `json:"id"`
	StopReason   string       `json:"stopReason"` // TODO enum
	StopSequence string       `json:"stopSequence"`
	Usage        Usage        `json:"usage"`
	Model        string       `json:"model"`
	Provider     ChatProvider `json:"provider"`
}

type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

/* based on openai's delta format */
type ChatMessageDelta struct {
	Role      ChatMessageRole `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []ToolCall      `json:"toolCalls"`
	Usage     Usage           `json:"usage"`
}

type ToolChoice struct {
	Type ToolChoiceType `json:"type"`
	Name string         `json:"name"`
}

type ToolChoiceType string

const (
	// llm will decide which tool to use, if any
	ToolChoiceTypeAuto        ToolChoiceType = "auto"
	ToolChoiceTypeUnspecified ToolChoiceType = ""

	// force to use one specific tool
	ToolChoiceTypeTool ToolChoiceType = "tool" // aka "function" in the openai API

	// force to use any one of the given tools
	ToolChoiceTypeRequired ToolChoiceType = "required" // aka "any" in the anthropic API
)

type ToolCall struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Parameters     *jsonschema.Schema `json:"parameters"`
	ParametersType reflect.Type       `json:"-"`
	// TODO: add field pointing to function to call for the tool call
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
	OpenaiToolChatProvider: "gpt-4o-mini",

	// NOTE: 3.5 Haiku is much more expensive than 3 Haiku, but performs better
	// too and is what claude presents as their "small" model
	AnthropicToolChatProvider: "claude-3-5-haiku-20241022",
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
