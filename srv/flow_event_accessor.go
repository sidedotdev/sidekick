package srv

import (
	"context"
	"time"

	"sidekick/domain"
)

// FlowEventAccessor provides an interface for adding flow events.
// FIXME /gen move to the domain package in domain/flow_event.go
type FlowEventAccessor interface {
	AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error
	EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error
	GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]domain.FlowEvent, map[string]string, error)
}
