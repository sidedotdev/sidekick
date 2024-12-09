package agent

// Event represents an event in the system.
type Event struct {
	Type string
	Data string
}

const (
	// EventTypeError represents an event where an error occurs.
	EventTypeError = "error"

	// EventTypeInferredIntent represents an event where a user intenant is inferred.
	EventTypeInferredIntent = "inferredIntent"
	EventTypeAction         = "action"
)
