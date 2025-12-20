package common

// Message is a common interface for chat messages across different implementations.
type Message interface {
	GetRole() string
	GetContentString() string
}

// MessageResponse is a common interface for LLM responses across different implementations.
// It provides access to the response message and metadata.
type MessageResponse interface {
	GetMessage() Message
	GetStopReason() string
	GetId() string
	GetInputTokens() int
	GetOutputTokens() int
}
