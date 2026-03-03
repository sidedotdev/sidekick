package testdata

import (
	"sidekick/domain"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

func violationTrackWithDCtx(dCtx flow_action.ExecContext) {
	actionCtx := dCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(dCtx, "something")
		return "", nil
	})
}

func violationTrackWithECtx(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(eCtx, "something")
		return "", nil
	})
}

func violationTrackHumanWithDCtx(actionCtx flow_action.ActionContext) {
	flow_action.TrackHuman(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(actionCtx, "something")
		return "", nil
	})
}

func violationTrackWithRenamedVar(myCtx flow_action.ExecContext) {
	actionCtx := myCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(myCtx, "something")
		return "", nil
	})
}

func violationTrackWithLocalAssign() {
	var eCtx flow_action.ExecContext
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(eCtx, "something")
		return "", nil
	})
}

func violationTrackWithOptionsActionCtx(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.TrackWithOptions(actionCtx, flow_action.TrackOptions{}, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (string, error) {
		workflow.ExecuteActivity(actionCtx, "something")
		return "", nil
	})
}