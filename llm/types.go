package llm

import "sidekick/common"

type ChatMessage = common.ChatMessage
type ChatMessageRole = common.ChatMessageRole

const (
	ChatMessageRoleUser      = common.ChatMessageRoleUser
	ChatMessageRoleAssistant = common.ChatMessageRoleAssistant
	ChatMessageRoleSystem    = common.ChatMessageRoleSystem
	ChatMessageRoleTool      = common.ChatMessageRoleTool
)

type ChatMessageResponse = common.ChatMessageResponse
type Usage = common.Usage
type ChatMessageDelta = common.ChatMessageDelta
type ToolChoice = common.ToolChoice
type ToolChoiceType = common.ToolChoiceType

const (
	ToolChoiceTypeAuto        = common.ToolChoiceTypeAuto
	ToolChoiceTypeUnspecified = common.ToolChoiceTypeUnspecified
	ToolChoiceTypeTool        = common.ToolChoiceTypeTool
	ToolChoiceTypeRequired    = common.ToolChoiceTypeRequired
)

type ToolCall = common.ToolCall
type Tool = common.Tool
