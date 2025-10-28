package llm2

import "context"

// Provider streams LLM responses as Events and returns a final MessageResponse.
// Providers MUST NOT close the eventChan; the caller owns the channel lifecycle.
type Provider interface {
	Stream(ctx context.Context, options Options, eventChan chan<- Event) (*MessageResponse, error)
}
