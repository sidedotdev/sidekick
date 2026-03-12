package llm2

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// NOTE: can't use anthropic.NewClient as it automatically injects
// default options based on env variables that may be out of sync with the
// options provided here explicitly
func newAnthropicClient(opts ...option.RequestOption) (r anthropic.Client) {
	opts = append([]option.RequestOption{option.WithEnvironmentProduction()}, opts...)

	r = anthropic.Client{Options: opts}

	r.Completions = anthropic.NewCompletionService(opts...)
	r.Messages = anthropic.NewMessageService(opts...)
	r.Models = anthropic.NewModelService(opts...)
	r.Beta = anthropic.NewBetaService(opts...)

	return
}
