package srv

import (
	"context"
	"time"

	"sidekick/flow_event"
)

// FlowEventAccessor provides an interface for adding flow events.
// FIXME /gen move to the domain package in domain/flow_event.go
type FlowEventAccessor interface {
	AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent flow_event.FlowEvent) error
	EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error
	GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]flow_event.FlowEvent, map[string]string, error)
}
