package temporalmeta

import (
	"context"
	"testing"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/sdk/converter"
)

// mockHistoryIterator implements client.HistoryEventIterator for testing.
type mockHistoryIterator struct {
	events []*historypb.HistoryEvent
	index  int
}

func (m *mockHistoryIterator) HasNext() bool {
	return m.index < len(m.events)
}

func (m *mockHistoryIterator) Next() (*historypb.HistoryEvent, error) {
	if m.index >= len(m.events) {
		return nil, nil
	}
	event := m.events[m.index]
	m.index++
	return event, nil
}

// mockTemporalClient implements only GetWorkflowHistory for testing.
type mockTemporalClient struct {
	iter *mockHistoryIterator
}

func (m *mockTemporalClient) GetWorkflowHistory(_ context.Context, _ string, _ string, _ bool, _ enumspb.HistoryEventFilterType) *mockHistoryIterator {
	return m.iter
}

func makeFlowActionIdHeader(flowActionId string) *commonpb.Header {
	payload, _ := converter.GetDefaultDataConverter().ToPayload(flowActionId)
	return &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			sidekickFlowActionIdHeaderKey: payload,
		},
	}
}

func makeScheduledEvent(eventId int64, activityType, activityId, flowActionId string) *historypb.HistoryEvent {
	var header *commonpb.Header
	if flowActionId != "" {
		header = makeFlowActionIdHeader(flowActionId)
	}
	return &historypb.HistoryEvent{
		EventId:   eventId,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
				ActivityType: &commonpb.ActivityType{Name: activityType},
				ActivityId:   activityId,
				TaskQueue:    &taskqueuepb.TaskQueue{Name: "test"},
				Header:       header,
			},
		},
	}
}

func makeCompletedEvent(eventId, scheduledEventId int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventId,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
				ScheduledEventId: scheduledEventId,
			},
		},
	}
}

func makeFailedEvent(eventId, scheduledEventId int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventId,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED,
		Attributes: &historypb.HistoryEvent_ActivityTaskFailedEventAttributes{
			ActivityTaskFailedEventAttributes: &historypb.ActivityTaskFailedEventAttributes{
				ScheduledEventId: scheduledEventId,
			},
		},
	}
}

func makeTimedOutEvent(eventId, scheduledEventId int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventId,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_ActivityTaskTimedOutEventAttributes{
			ActivityTaskTimedOutEventAttributes: &historypb.ActivityTaskTimedOutEventAttributes{
				ScheduledEventId: scheduledEventId,
			},
		},
	}
}

func makeCanceledEvent(eventId, scheduledEventId int64) *historypb.HistoryEvent {
	return &historypb.HistoryEvent{
		EventId:   eventId,
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCanceledEventAttributes{
			ActivityTaskCanceledEventAttributes: &historypb.ActivityTaskCanceledEventAttributes{
				ScheduledEventId: scheduledEventId,
			},
		},
	}
}

// testableActivities wraps TemporalMetaActivities but uses a mock iterator
// directly, since the real client.Client interface is large. We test the
// core logic by constructing the iterator ourselves and calling the function
// that processes it.
//
// Because FetchFlowActionActivities calls t.Client.GetWorkflowHistory which
// returns client.HistoryEventIterator, we test via the exported function by
// providing a real client mock. Instead, we extract the logic into a testable
// helper or test using a full interface mock.
//
// Given the Temporal client interface is very large, we'll test via a
// refactored approach: extract the iterator processing into a separate
// function that we can test directly.

func TestFetchFlowActionActivities_HeaderMatching(t *testing.T) {
	t.Parallel()

	targetFlowActionId := "fa_target123"
	otherFlowActionId := "fa_other456"

	events := []*historypb.HistoryEvent{
		// Matching scheduled event
		makeScheduledEvent(1, "DoSomething", "act-1", targetFlowActionId),
		// Non-matching scheduled event (different flow action ID)
		makeScheduledEvent(2, "DoOtherThing", "act-2", otherFlowActionId),
		// Scheduled event with no header at all
		makeScheduledEvent(3, "NoHeader", "act-3", ""),
		// Completion events for both
		makeCompletedEvent(4, 1),
		makeCompletedEvent(5, 2),
	}

	iter := &mockHistoryIterator{events: events}
	results, err := processHistoryIterator(iter, targetFlowActionId)
	require.NoError(t, err)

	require.Len(t, results, 1)
	assert.Equal(t, "DoSomething", results[0].ActivityType)
	assert.Equal(t, "act-1", results[0].ActivityId)
	assert.Equal(t, int64(1), results[0].ScheduledEventId)
	assert.Equal(t, int64(4), results[0].CloseEventId)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED.String(), results[0].CloseEventType)
}

func TestFetchFlowActionActivities_JoinCloseEvents(t *testing.T) {
	t.Parallel()

	flowActionId := "fa_join_test"

	events := []*historypb.HistoryEvent{
		makeScheduledEvent(10, "ActivityA", "act-a", flowActionId),
		makeScheduledEvent(11, "ActivityB", "act-b", flowActionId),
		makeScheduledEvent(12, "ActivityC", "act-c", flowActionId),
		makeScheduledEvent(13, "ActivityD", "act-d", flowActionId),
		makeCompletedEvent(20, 10),
		makeFailedEvent(21, 11),
		makeTimedOutEvent(22, 12),
		makeCanceledEvent(23, 13),
	}

	iter := &mockHistoryIterator{events: events}
	results, err := processHistoryIterator(iter, flowActionId)
	require.NoError(t, err)

	require.Len(t, results, 4)

	byActivity := map[string]domain.TemporalActivityRef{}
	for _, r := range results {
		byActivity[r.ActivityType] = r
	}

	// ActivityA -> completed
	assert.Equal(t, int64(10), byActivity["ActivityA"].ScheduledEventId)
	assert.Equal(t, int64(20), byActivity["ActivityA"].CloseEventId)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED.String(), byActivity["ActivityA"].CloseEventType)

	// ActivityB -> failed
	assert.Equal(t, int64(11), byActivity["ActivityB"].ScheduledEventId)
	assert.Equal(t, int64(21), byActivity["ActivityB"].CloseEventId)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED.String(), byActivity["ActivityB"].CloseEventType)

	// ActivityC -> timed out
	assert.Equal(t, int64(12), byActivity["ActivityC"].ScheduledEventId)
	assert.Equal(t, int64(22), byActivity["ActivityC"].CloseEventId)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT.String(), byActivity["ActivityC"].CloseEventType)

	// ActivityD -> canceled
	assert.Equal(t, int64(13), byActivity["ActivityD"].ScheduledEventId)
	assert.Equal(t, int64(23), byActivity["ActivityD"].CloseEventId)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED.String(), byActivity["ActivityD"].CloseEventType)
}

func TestFetchFlowActionActivities_ScheduledWithoutClose(t *testing.T) {
	t.Parallel()

	flowActionId := "fa_no_close"

	events := []*historypb.HistoryEvent{
		makeScheduledEvent(1, "PendingActivity", "act-1", flowActionId),
		// No close event for this scheduled event
	}

	iter := &mockHistoryIterator{events: events}
	results, err := processHistoryIterator(iter, flowActionId)
	require.NoError(t, err)

	require.Len(t, results, 1)
	assert.Equal(t, "PendingActivity", results[0].ActivityType)
	assert.Equal(t, int64(1), results[0].ScheduledEventId)
	assert.Equal(t, int64(0), results[0].CloseEventId)
	assert.Equal(t, "", results[0].CloseEventType)
}

func TestFetchFlowActionActivities_NoMatchingEvents(t *testing.T) {
	t.Parallel()

	events := []*historypb.HistoryEvent{
		makeScheduledEvent(1, "Unrelated", "act-1", "fa_other"),
		makeCompletedEvent(2, 1),
	}

	iter := &mockHistoryIterator{events: events}
	results, err := processHistoryIterator(iter, "fa_nonexistent")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFetchFlowActionActivities_EmptyHistory(t *testing.T) {
	t.Parallel()

	iter := &mockHistoryIterator{events: nil}
	results, err := processHistoryIterator(iter, "fa_anything")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFetchFlowActionActivities_MultipleMatchingWithMixed(t *testing.T) {
	t.Parallel()

	targetId := "fa_mixed"

	events := []*historypb.HistoryEvent{
		makeScheduledEvent(1, "MatchA", "act-1", targetId),
		makeScheduledEvent(2, "NoMatch", "act-2", "fa_other"),
		makeScheduledEvent(3, "MatchB", "act-3", targetId),
		makeCompletedEvent(4, 1),
		makeCompletedEvent(5, 2), // close for non-matching
		makeFailedEvent(6, 3),
	}

	iter := &mockHistoryIterator{events: events}
	results, err := processHistoryIterator(iter, targetId)
	require.NoError(t, err)

	require.Len(t, results, 2)
	byActivity := map[string]domain.TemporalActivityRef{}
	for _, r := range results {
		byActivity[r.ActivityType] = r
	}
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED.String(), byActivity["MatchA"].CloseEventType)
	assert.Equal(t, enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED.String(), byActivity["MatchB"].CloseEventType)
}
