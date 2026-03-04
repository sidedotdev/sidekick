package violations

import (
	"sidekick/flow_action"
	"sidekick/workflow"
)

func violationTrackWithDCtx(dCtx flow_action.ExecContext) {
	actionCtx := dCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(dCtx, "something") // want `"dCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackWithECtx(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(eCtx, "something") // want `"eCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackHumanWithActionCtx(actionCtx flow_action.ActionContext) {
	flow_action.TrackHuman(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(actionCtx, "something") // want `"actionCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackWithRenamedVar(myCtx flow_action.ExecContext) {
	actionCtx := myCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(myCtx, "something") // want `"myCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackWithLocalAssign() {
	var eCtx flow_action.ExecContext
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(eCtx, "something") // want `"eCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackWithOptionsActionCtx(eCtx flow_action.ExecContext) {
	actionCtx := eCtx.NewActionContext("test")
	flow_action.TrackWithOptions(actionCtx, flow_action.TrackOptions{}, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(actionCtx, "something") // want `"actionCtx" referenced in Track callback`
		return "", nil
	})
}

func violationTrackWithWorkflowContext(ctx workflow.Context) {
	var eCtx flow_action.ExecContext
	actionCtx := eCtx.NewActionContext("test")
	flow_action.Track(actionCtx, func(trackedCtx flow_action.ActionContext, flowAction *flow_action.FlowAction) (string, error) {
		workflow.ExecuteActivity(ctx, "something") // want `"ctx" referenced in Track callback`
		return "", nil
	})
}