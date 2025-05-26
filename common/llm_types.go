package common

import (
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"
)

type ChatMessage struct {
	Role      ChatMessageRole `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []ToolCall      `json:"toolCalls"`

	/* for tool call responses */
	Name       string `json:"name"`
	ToolCallId string `json:"toolCallId"`
	IsError    bool   `json:"isError"`

	/* temporary, until we move to using a slice of content blocks */
	CacheControl string `json:"cacheControl"`
	ContextType  string `json:"contextType,omitempty"`
}

// TODO ChatMessage.Content should be changed to []ContentBlock
type ContentBlock struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	CacheControl string `json:"cacheControl"`
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
	Id           string `json:"id"`
	StopReason   string `json:"stopReason"` // TODO enum
	StopSequence string `json:"stopSequence"`
	Usage        Usage  `json:"usage"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
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
	UnspecifiedChatProvider      ChatProvider = ""
	OpenaiChatProvider           ChatProvider = "openai"
	AnthropicChatProvider        ChatProvider = "anthropic"
	OpenaiCompatibleChatProvider ChatProvider = "openai_compatible"
	GoogleChatProvider           ChatProvider = "google"
)

type ToolChatProviderType string

const (
	UnspecifiedToolChatProviderType      ToolChatProviderType = ""
	OpenaiToolChatProviderType           ToolChatProviderType = "openai"
	AnthropicToolChatProviderType        ToolChatProviderType = "anthropic"
	GoogleToolChatProviderType           ToolChatProviderType = "google"
	OpenaiCompatibleToolChatProviderType ToolChatProviderType = "openai_compatible"
)

var SmallModels = map[ToolChatProviderType]string{
	OpenaiToolChatProviderType: "gpt-4.1-mini-2025-04-14",

	// NOTE: 3.5 Haiku is much more expensive than 3 Haiku, but performs better
	// too and is what claude presents as their "small" model
	AnthropicToolChatProviderType: "claude-3-5-haiku-20241022",

	GoogleToolChatProviderType: "gemini-2.5-flash-preview-04-17",
}

func (provider ToolChatProviderType) SmallModel() string {
	// missing will be empty string, i.e. the internal/built-in default model
	// for the provider integration implementation
	return SmallModels[provider]
}

func StringToToolChatProviderType(providerType string) (ToolChatProviderType, error) {
	switch providerType {
	case string(OpenaiToolChatProviderType):
		return OpenaiToolChatProviderType, nil
	case string(AnthropicToolChatProviderType):
		return AnthropicToolChatProviderType, nil
	case string(GoogleToolChatProviderType):
		return GoogleToolChatProviderType, nil
	case string(UnspecifiedToolChatProviderType):
		return UnspecifiedToolChatProviderType, nil
	case string(OpenaiCompatibleToolChatProviderType):
		return OpenaiCompatibleToolChatProviderType, nil
	default:
		return UnspecifiedToolChatProviderType, fmt.Errorf("unknown provider: %s", providerType)
	}
}
