package chathistory

// Message is a stub message type.
type Message struct{}

// ChatHistoryContainer mirrors persisted_ai.ChatHistoryContainer.
type ChatHistoryContainer struct{}

// Append adds a message to the chat history.
func (c *ChatHistoryContainer) Append(msg Message) {}