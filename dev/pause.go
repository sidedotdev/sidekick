package dev

import (
	"go.temporal.io/sdk/workflow"
)

func SetupPauseHandler(ctx workflow.Context, globalState *GlobalState) {
	signalChan := workflow.GetSignalChannel(ctx, "pause-workflow")
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				var signal interface{}
				c.Receive(ctx, &signal)
				globalState.Paused = true
			})
			selector.Select(ctx)
		}
	})
}
