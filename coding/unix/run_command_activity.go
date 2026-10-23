package unix

import (
	"context"

	"sidekick/env/localio"
)

type RunCommandActivityInput = localio.RunCommandInput

type RunCommandActivityOutput = localio.RunCommandOutput

func RunCommandActivity(ctx context.Context, input RunCommandActivityInput) (RunCommandActivityOutput, error) {
	return localio.RunCommand(ctx, input)
}
