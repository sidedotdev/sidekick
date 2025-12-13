package common

// Message is a common interface for chat messages across different implementations.
type Message interface {
	GetRole() string
	GetContentString() string
}
