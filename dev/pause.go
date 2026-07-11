package dev

import (
	"fmt"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

type Pause struct{}

func SetupPauseHandler(dCtx DevContext, guidanceContext string, requestParams map[string]interface{}) {
	signalChan := workflow.GetSignalChannel(dCtx, SignalNamePause)
	workflow.Go(dCtx, func(ctx workflow.Context) {
		for {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				c.Receive(ctx, &Pause{})
				dCtx.GlobalState.Paused = true
				dCtx.GlobalState.Cancel() // cancel any ongoing activities when paused
			})
			selector.Select(ctx)
		}
	})
}

func UserRequestIfPaused(dCtx DevContext, guidanceContext string, requestParams map[string]interface{}) (*flow_action.UserResponse, error) {
	if dCtx.ExecContext.GlobalState == nil || !dCtx.ExecContext.GlobalState.Paused {
		return nil, nil
	}

	guidanceRequest := &flow_action.RequestForUser{
		OriginWorkflowId: workflow.GetInfo(dCtx).WorkflowExecution.ID,
		Subflow:          dCtx.FlowScope.SubflowName,
		SubflowId:        dCtx.FlowScope.GetSubflowId(),
		Content:          guidanceContext,
		RequestKind:      flow_action.RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	actionCtx := dCtx.NewActionContext("user_request.paused")
	actionCtx.ActionParams = guidanceRequest.ActionParams()

	response, err := GetUserResponse(actionCtx, *guidanceRequest)

	dCtx.ExecContext.GlobalState.Paused = false

	// Restore hibernated worktree after the user signals to resume, not before,
	// so the worktree stays hibernated while waiting for user input.
	hibernateVersion := workflow.GetVersion(dCtx, "hibernate-worktree", workflow.DefaultVersion, 3)
	if err == nil && hibernateVersion == 2 {
		clearHibernationGlobalState(dCtx)
	} else if err == nil {
		if _, wakeErr := WakeIfHibernated(dCtx); wakeErr != nil {
			return nil, fmt.Errorf("failed to wake hibernated worktree: %w", wakeErr)
		}
	}

	return response, err
}
