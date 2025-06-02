package dev

import (
	"sidekick/domain"

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

func UserRequestIfPaused(dCtx DevContext, guidanceContext string, requestParams map[string]interface{}) (*UserResponse, error) {
	if dCtx.GlobalState == nil || !dCtx.GlobalState.Paused {
		return nil, nil
	}

	guidanceRequest := &RequestForUser{
		OriginWorkflowId: workflow.GetInfo(dCtx).WorkflowExecution.ID,
		Subflow:          dCtx.FlowScope.SubflowName,
		Content:          guidanceContext,
		RequestKind:      RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	actionCtx := dCtx.NewActionContext("user_request.paused")
	actionCtx.ActionParams = guidanceRequest.ActionParams()

	response, err := TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		guidanceRequest.FlowActionId = flowAction.Id
		return GetUserResponse(dCtx, *guidanceRequest)
	})

	dCtx.GlobalState.Paused = false
	return response, err
}
