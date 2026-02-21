package callers

import (
	"fixture/chathistory"
	"fixture/persisted_ai"
	"fixture/workflow"
)

// WorkflowCallsSanctioned calls the sanctioned AppendChatHistory wrapper.
func WorkflowCallsSanctioned(ctx workflow.Context, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	persisted_ai.AppendChatHistory(h, msg)
}