package llm

import (
	"reflect"

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

// Only listing the models one might use that have short context lengths
var ShortContextLengthByModel = map[string]int{
	openai.GPT4:     35000,
	openai.GPT40314: 35000,
	openai.GPT40613: 35000,
}

func MaxContextChars(model string) int {
	maxChars := ShortContextLengthByModel[model]
	if maxChars == 0 {
		return 200000
	}
	return maxChars
}
