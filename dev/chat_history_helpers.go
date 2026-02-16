package dev

import (
	"sidekick/common"
	"sidekick/persisted_ai"

	"go.temporal.io/sdk/workflow"
)

// NewVersionedChatHistory creates a ChatHistoryContainer using the appropriate
// implementation based on workflow versioning. Version 1 uses Llm2ChatHistory
// with KV storage support, while the default version uses LegacyChatHistory.
func NewVersionedChatHistory(ctx workflow.Context, workspaceId string) *persisted_ai.ChatHistoryContainer {
	v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)
	if v == 1 {
		flowId := workflow.GetInfo(ctx).WorkflowExecution.ID
		return &persisted_ai.ChatHistoryContainer{
			History: persisted_ai.NewLlm2ChatHistory(flowId, workspaceId),
		}
	}
	return &persisted_ai.ChatHistoryContainer{
		History: persisted_ai.NewLegacyChatHistoryFromChatMessages(nil),
	}
}

// AppendChatHistory appends a message to chat history. For llm2 history, it
// persists the message to KV storage via an activity and appends only the ref.
// For legacy history, it appends directly.
func AppendChatHistory(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, msg common.Message) {
	persisted_ai.AppendChatHistory(ctx, chatHistory, msg)
}
