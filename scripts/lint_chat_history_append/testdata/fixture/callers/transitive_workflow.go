package callers

import (
	"fixture/chathistory"
	"fixture/workflow"
)

// TransitiveWorkflowCaller has workflow.Context and calls HelperAppend (not Append directly).
func TransitiveWorkflowCaller(ctx workflow.Context, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	HelperAppend(h, msg)
}