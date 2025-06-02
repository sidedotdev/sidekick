package dev

import (
	"go.temporal.io/sdk/workflow"
)

// SetupUserActionHandler sets up a signal handler for user actions like "go_next_step".
// It listens on the "user_action" signal channel. When a UserActionGoNext is received,
// it updates the GlobalState.
func SetupUserActionHandler(dCtx DevContext) {
	signalChan := workflow.GetSignalChannel(dCtx, SignalNameUserAction)

	workflow.Go(dCtx, func(ctx workflow.Context) {
		for {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				var action UserActionType
				c.Receive(ctx, &action) // Receive the UserActionType from the signal

				// Check if the received action is UserActionGoNext
				if action == UserActionGoNext {
					dCtx.GlobalState.SetUserAction(UserActionGoNext)
				}
				// Other actions could be handled here in the future with an else if or switch.
			})
			selector.Select(ctx)
			if ctx.Err() != nil { // Exit goroutine if context is done
				return
			}
		}
	})
}
