package dev

import (
	"sidekick/common"
	"sidekick/temp_common2"

	"go.temporal.io/sdk/workflow"
)

// NewVersionedChatHistory creates a ChatHistoryContainer using the appropriate
// implementation based on workflow versioning. Version 1 uses Llm2ChatHistory
// with KV storage support, while the default version uses LegacyChatHistory.
func NewVersionedChatHistory(ctx workflow.Context, workspaceId string) *common.ChatHistoryContainer {
	v := workflow.GetVersion(ctx, "chat-history-llm2", workflow.DefaultVersion, 1)
	if v == 1 {
		flowId := workflow.GetInfo(ctx).WorkflowExecution.ID
		return &common.ChatHistoryContainer{
			History: temp_common2.NewLlm2ChatHistory(flowId, workspaceId),
		}
	}
	return &common.ChatHistoryContainer{
		History: common.NewLegacyChatHistoryFromChatMessages(nil),
	}
}
