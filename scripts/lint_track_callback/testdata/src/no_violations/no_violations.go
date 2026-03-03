package no_violations

import (
	"sidekick/flow_action"
	"sidekick/workflow"
)

func correctUsesTrackedCtxParam(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(trackedCtx, "something")
		return "", nil
	})
}

func correctTrackHuman(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.TrackHuman(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(trackedCtx, "something")
		return "", nil
	})
}

func correctTrackWithOptions(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.TrackWithOptions(actionCtx, flow_action.TrackOptions{}, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(trackedCtx, "something")
		return "", nil
	})
}

func correctNonContextVarFromOuter(eCtx flow_action.ExecContext) {
	someStr := "hello"
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(trackedCtx, someStr)
		return "", nil
	})
}