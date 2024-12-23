package flow_action

import (
	"context"

	"sidekick/domain"
	"sidekick/srv"
)

type FlowActivities struct {
	Service srv.Service
}

func (fa *FlowActivities) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	return fa.Service.PersistFlowAction(ctx, flowAction)
}

func (fa *FlowActivities) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	return fa.Service.PersistSubflow(ctx, subflow)
}
