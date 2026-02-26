package temporalmeta

import (
	"context"

	"sidekick/domain"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

const sidekickFlowActionIdHeaderKey = "sidekickFlowActionId"

type TemporalMetaActivities struct {
	Client client.Client
}

type FetchFlowActionActivitiesParams struct {
	WorkflowId   string `json:"workflowId"`
	RunId        string `json:"runId"`
	FlowActionId string `json:"flowActionId"`
}

func (t *TemporalMetaActivities) FetchFlowActionActivities(ctx context.Context, params FetchFlowActionActivitiesParams) ([]domain.TemporalActivityRef, error) {
	iter := t.Client.GetWorkflowHistory(ctx, params.WorkflowId, params.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

	type scheduledInfo struct {
		activityType string
		activityId   string
		eventId      int64
	}

	// First pass: collect all events, identify scheduled events matching our flow action ID
	matchingScheduled := map[int64]scheduledInfo{} // keyed by scheduled event ID
	type closeInfo struct {
		scheduledEventId int64
		eventId          int64
		eventType        string
	}
	var closeEvents []closeInfo

	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, err
		}

		switch event.EventType {
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs == nil || attrs.Header == nil {
				continue
			}
			payload, ok := attrs.Header.Fields[sidekickFlowActionIdHeaderKey]
			if !ok || payload == nil {
				continue
			}
			var flowActionId string
			if err := converter.GetDefaultDataConverter().FromPayload(payload, &flowActionId); err != nil {
				continue
			}
			if flowActionId != params.FlowActionId {
				continue
			}
			matchingScheduled[event.EventId] = scheduledInfo{
				activityType: attrs.ActivityType.GetName(),
				activityId:   attrs.ActivityId,
				eventId:      event.EventId,
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if attrs := event.GetActivityTaskCompletedEventAttributes(); attrs != nil {
				closeEvents = append(closeEvents, closeInfo{
					scheduledEventId: attrs.ScheduledEventId,
					eventId:          event.EventId,
					eventType:        event.EventType.String(),
				})
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			if attrs := event.GetActivityTaskFailedEventAttributes(); attrs != nil {
				closeEvents = append(closeEvents, closeInfo{
					scheduledEventId: attrs.ScheduledEventId,
					eventId:          event.EventId,
					eventType:        event.EventType.String(),
				})
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			if attrs := event.GetActivityTaskTimedOutEventAttributes(); attrs != nil {
				closeEvents = append(closeEvents, closeInfo{
					scheduledEventId: attrs.ScheduledEventId,
					eventId:          event.EventId,
					eventType:        event.EventType.String(),
				})
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
			if attrs := event.GetActivityTaskCanceledEventAttributes(); attrs != nil {
				closeEvents = append(closeEvents, closeInfo{
					scheduledEventId: attrs.ScheduledEventId,
					eventId:          event.EventId,
					eventType:        event.EventType.String(),
				})
			}
		}
	}

	if len(matchingScheduled) == 0 {
		return nil, nil
	}

	// Index close events by their scheduled event ID
	closeByScheduled := map[int64]closeInfo{}
	for _, ce := range closeEvents {
		closeByScheduled[ce.scheduledEventId] = ce
	}

	// Build result by joining scheduled with close events
	var results []domain.TemporalActivityRef
	for _, sched := range matchingScheduled {
		ref := domain.TemporalActivityRef{
			ActivityType:     sched.activityType,
			ActivityId:       sched.activityId,
			ScheduledEventId: sched.eventId,
		}
		if ce, ok := closeByScheduled[sched.eventId]; ok {
			ref.CloseEventId = ce.eventId
			ref.CloseEventType = ce.eventType
		}
		results = append(results, ref)
	}

	return results, nil
}
