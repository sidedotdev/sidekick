package callers

import "fixture/chathistory"

// ExecContextCaller has an ExecContext parameter and calls Append.
func ExecContextCaller(ec ExecContext, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}