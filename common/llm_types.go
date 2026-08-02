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

// GetRole returns the role as a string.
func (c ChatMessage) GetRole() string {
	return string(c.Role)
}

func (c ChatMessage) GetToolCalls() []ToolCall {
	return c.ToolCalls
}

// GetContentString returns the content string.
func (c ChatMessage) GetContentString() string {
	return c.Content
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

	// the reasoning effort actually applied for the provider (may not match the
	// effort requested if the model does not support reasoning or uses a
	// different effort enum/schema)
	ReasoningEffort string `json:"reasoningEffort"`
}

// InputTokens must be the total prompt tokens (cached + non-cached).
// CacheReadInputTokens and CacheWriteInputTokens are subsets of InputTokens.
type Usage struct {
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
}

// GetMessage returns the embedded ChatMessage as a Message interface.
func (r *ChatMessageResponse) GetMessage() Message {
	return r.ChatMessage
}

// GetStopReason returns the stop reason.
func (r *ChatMessageResponse) GetStopReason() string {
	return r.StopReason
}

// GetId returns the response ID.
func (r *ChatMessageResponse) GetId() string {
	return r.Id
}

// GetInputTokens returns the number of input tokens used.
func (r *ChatMessageResponse) GetInputTokens() int {
	return r.Usage.InputTokens
}

// GetOutputTokens returns the number of output tokens used.
func (r *ChatMessageResponse) GetOutputTokens() int {
	return r.Usage.OutputTokens
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
	Signature []byte `json:"signature"`
}

// ToolType discriminates client-side function tools from provider-native
// (server-side) builtin tools within a single tools list, mirroring how
// providers accept both kinds of tools in one request-level tool list.
type ToolType string

const (
	// The zero value is treated as a function tool for backward compatibility
	// with persisted tool lists that predate ToolType.
	ToolTypeFunction ToolType = "function"
	// ToolTypeWebSearch enables the provider-native web search tool. Name,
	// Description and Parameters are ignored for this type: each provider
	// emits its own native tool name/type.
	ToolTypeWebSearch ToolType = "web_search"
)

// WebSearchToolConfig configures the provider-native web search tool. Fields
// that a provider does not support are ignored by that provider.
type WebSearchToolConfig struct {
	// MaxUses limits how many searches may run in one request (Anthropic only).
	MaxUses int `json:"maxUses,omitempty"`
	// AllowedDomains restricts search results to the given domains
	// (Anthropic allowed_domains, OpenAI filters.allowed_domains).
	AllowedDomains []string `json:"allowedDomains,omitempty"`
}

type Tool struct {
	Type           ToolType           `json:"type,omitempty"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Parameters     *jsonschema.Schema `json:"parameters"`
	ParametersType reflect.Type       `json:"-"`
	// TODO: add field pointing to function to call for the tool call

	// WebSearch optionally configures the web search tool when Type is
	// ToolTypeWebSearch; nil means provider defaults.
	WebSearch *WebSearchToolConfig `json:"webSearch,omitempty"`
}

// IsFunction reports whether the tool is a client-side function tool.
func (t Tool) IsFunction() bool {
	return t.Type == "" || t.Type == ToolTypeFunction
}

type ChatProvider string

const (
	UnspecifiedChatProvider               ChatProvider = ""
	OpenaiChatProvider                    ChatProvider = "openai"
	AnthropicChatProvider                 ChatProvider = "anthropic"
	OpenaiCompatibleChatProvider          ChatProvider = "openai_compatible"
	OpenaiResponsesCompatibleChatProvider ChatProvider = "openai_responses_compatible"
	GoogleChatProvider                    ChatProvider = "google"
	BedrockChatProvider                   ChatProvider = "bedrock"
)

type ToolChatProviderType string

const (
	UnspecifiedToolChatProviderType      ToolChatProviderType = ""
	OpenaiToolChatProviderType           ToolChatProviderType = "openai"
	AnthropicToolChatProviderType        ToolChatProviderType = "anthropic"
	GoogleToolChatProviderType           ToolChatProviderType = "google"
	OpenaiCompatibleToolChatProviderType ToolChatProviderType = "openai_compatible"
	BedrockToolChatProviderType          ToolChatProviderType = "bedrock"
)

var SmallModels = map[ToolChatProviderType]string{
	OpenaiToolChatProviderType: "gpt-5-mini-2025-08-07",

	AnthropicToolChatProviderType: "claude-haiku-4-5",

	GoogleToolChatProviderType: "gemini-3-flash-preview",

	BedrockToolChatProviderType: "global.anthropic.claude-haiku-4-5-20251001-v1:0",
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
	case string(BedrockToolChatProviderType):
		return BedrockToolChatProviderType, nil
	default:
		return UnspecifiedToolChatProviderType, fmt.Errorf("unknown provider: %s", providerType)
	}
}
