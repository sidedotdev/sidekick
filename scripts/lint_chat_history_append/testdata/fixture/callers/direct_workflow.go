package callers

import (
	"fixture/chathistory"
	"fixture/workflow"
)

// DirectWorkflowCaller has a workflow.Context parameter and calls Append directly.
func DirectWorkflowCaller(ctx workflow.Context, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}