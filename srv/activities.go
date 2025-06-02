package srv

import (
	"context"
	"sidekick/domain"
)

// Delegates to a Service. we don't use a Service directly as an activity,
// mainly as activities require specific number of return values and other
// expectations around backwards compatibility that our service layer doesn't
// need to adhere to. Not all methods in the service layer are expected to be
// available as activities.
type Activities struct {
	Service Service
}

func (a Activities) PersistWorktree(ctx context.Context, worktree domain.Worktree) error {
	return a.Service.PersistWorktree(ctx, worktree)
}

func (a Activities) PersistFlow(ctx context.Context, workflow domain.Flow) error {
	return a.Service.PersistFlow(ctx, workflow)
}

func (a Activities) GetFlow(ctx context.Context, workspaceId string, flowId string) (domain.Flow, error) {
	return a.Service.GetFlow(ctx, workspaceId, flowId)
}

func (a Activities) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEventContainer domain.FlowEventContainer) error {
	flowEvent := flowEventContainer.FlowEvent
	// The underlying service method still expects the FlowEvent interface, not the container.
	return a.Service.AddFlowEvent(ctx, workspaceId, flowId, flowEvent)
}

// TODO: consider adding these as needed, though we'd ideally change the inputs to structs some of these for backcompat
/*
func (a Activities) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	return a.Service.PersistSubflow(ctx, subflow)
}

func (a Activities) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	err := a.Service.PersistFlowAction(ctx, flowAction)
	if err != nil {
		return err
	}
	return a.Service.AddFlowActionChange(ctx, flowAction)
}

func (a Activities) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	return a.Service.EndFlowEventStream(ctx, workspaceId, flowId, eventStreamParentId)
}
*/
