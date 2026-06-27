package persisted_ai

import (
	"context"

	"sidekick/llm"
)

// RepairToolCallArgsInput carries the raw JSON argument strings of a message's
// tool calls to be repaired.
type RepairToolCallArgsInput struct {
	Arguments []string
}

// RepairToolCallArgsOutput holds the repaired JSON argument strings,
// positionally aligned with the input arguments.
type RepairToolCallArgsOutput struct {
	Arguments []string
}

// RepairToolCallArgumentsActivity applies the full JSON repair pass to each
// tool-call argument string. Running this as a dedicated activity within the
// flow action keeps the streaming activities pure while ensuring the repaired
// arguments are what gets recorded as the flow action result.
func RepairToolCallArgumentsActivity(ctx context.Context, input RepairToolCallArgsInput) (RepairToolCallArgsOutput, error) {
	repaired := make([]string, len(input.Arguments))
	for i, arg := range input.Arguments {
		repaired[i] = llm.RepairJsonFull(arg)
	}
	return RepairToolCallArgsOutput{Arguments: repaired}, nil
}
