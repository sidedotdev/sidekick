package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
)

func TestEventInputPayloads(t *testing.T) {
	t.Parallel()

	input := &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("in")}}}

	tests := []struct {
		name  string
		event *historypb.HistoryEvent
		want  *commonpb.Payloads
	}{
		{
			name: "activity scheduled",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
				Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
					ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{Input: input},
				},
			},
			want: input,
		},
		{
			name: "workflow started",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
					WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{Input: input},
				},
			},
			want: input,
		},
		{
			name: "signaled",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionSignaledEventAttributes{
					WorkflowExecutionSignaledEventAttributes: &historypb.WorkflowExecutionSignaledEventAttributes{Input: input},
				},
			},
			want: input,
		},
		{
			name: "activity completed has no input",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
				Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
					ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, eventInputPayloads(tt.event))
		})
	}
}

func TestEventOutputPayloads(t *testing.T) {
	t.Parallel()

	result := &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("out")}}}

	tests := []struct {
		name  string
		event *historypb.HistoryEvent
		want  *commonpb.Payloads
	}{
		{
			name: "activity completed",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
				Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
					ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{Result: result},
				},
			},
			want: result,
		},
		{
			name: "workflow completed",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
					WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: result},
				},
			},
			want: result,
		},
		{
			name: "activity scheduled has no output",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
				Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
					ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, eventOutputPayloads(tt.event))
		})
	}
}

func TestCompletionSourceEventId(t *testing.T) {
	t.Parallel()

	activityCompleted := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 42},
		},
	}
	assert.Equal(t, int64(42), completionSourceEventId(activityCompleted))

	childCompleted := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
			ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{InitiatedEventId: 7},
		},
	}
	assert.Equal(t, int64(7), completionSourceEventId(childCompleted))

	scheduled := &historypb.HistoryEvent{EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED}
	assert.Equal(t, int64(0), completionSourceEventId(scheduled))
}

func TestDecodePayloads(t *testing.T) {
	t.Parallel()

	dc := converter.GetDefaultDataConverter()

	t.Run("nil payloads", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, decodePayloads(dc, nil))
		assert.NotNil(t, decodePayloads(dc, nil))
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		payloads, err := dc.ToPayloads(map[string]interface{}{"greeting": "hello"}, 123)
		require.NoError(t, err)

		decoded := decodePayloads(dc, payloads)
		require.Len(t, decoded, 2)
		assert.Equal(t, map[string]interface{}{"greeting": "hello"}, decoded[0])
		assert.Equal(t, float64(123), decoded[1])
	})
}
