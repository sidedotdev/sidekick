package llm2

import (
	"context"
	"sidekick/secret_manager"
)

// StreamRequest carries messages, options, and secrets to a Provider.
// SecretManager is an interface so llm2 has no dependency on serialization.
type StreamRequest struct {
	Messages      []Message
	Options       Options
	SecretManager secret_manager.SecretManager
}

// Provider streams LLM responses as Events and returns a final MessageResponse.
// Providers MUST NOT close the eventChan; the caller owns the channel lifecycle.
type Provider interface {
	Stream(ctx context.Context, request StreamRequest, eventChan chan<- Event) (*MessageResponse, error)
}
