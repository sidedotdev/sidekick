package callers

import "fixture/chathistory"

// DevContextCaller has a DevContext parameter and calls Append.
func DevContextCaller(dCtx DevContext, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}