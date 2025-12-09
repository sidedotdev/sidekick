package dev

import (
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
		Content:          guidanceContext,
		RequestKind:      flow_action.RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	actionCtx := dCtx.NewActionContext("user_request.paused")
	actionCtx.ActionParams = guidanceRequest.ActionParams()

	response, err := GetUserResponse(actionCtx, *guidanceRequest)

	dCtx.ExecContext.GlobalState.Paused = false
	return response, err
}
