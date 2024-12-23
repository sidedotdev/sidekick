package flow_action

import (
	"context"

	"sidekick/domain"
	"sidekick/srv/redis"
)

type FlowActivities struct {
	// TODO /gen replace with a new Accessor interface added to flow_action package
	DatabaseAccessor *redis.Service
}

func (fa *FlowActivities) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	return fa.DatabaseAccessor.PersistFlowAction(ctx, flowAction)
}

func (fa *FlowActivities) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	return fa.DatabaseAccessor.PersistSubflow(ctx, subflow)
}
