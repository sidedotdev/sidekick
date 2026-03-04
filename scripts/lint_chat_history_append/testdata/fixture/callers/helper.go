package callers

import "fixture/chathistory"

// HelperAppend is a plain helper with no workflow context that calls Append.
func HelperAppend(h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}