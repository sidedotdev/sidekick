package env

import "context"

// EnvRunCommandActivity runs a command without the worker's injected
// dependencies, for callers already inside another activity. Workflows
// schedule the Activities method of the same name instead, which is what the
// worker registers.
func EnvRunCommandActivity(ctx context.Context, input EnvRunCommandActivityInput) (EnvRunCommandActivityOutput, error) {
	return (&Activities{}).RunCommand(ctx, input)
}
