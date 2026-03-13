package persisted_ai

import "fixture/chathistory"

// AppendChatHistory is the sanctioned wrapper for calling Append.
func AppendChatHistory(h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}