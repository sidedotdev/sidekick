package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sidekick/temporal"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
)

func inlinePayload(size int) *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     bytes.Repeat([]byte("x"), size),
	}}}
}

func offloadedPayload() *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{{
		Metadata: map[string][]byte{codecMetadataKey: []byte("codec/abc")},
	}}}
}

// The stats tool must read the exact snapshot format written by
// `worker/replay -history-cache`, header included.
func TestReadSnapshot_AcceptsReplaySnapshotFormat(t *testing.T) {
	t.Parallel()
	hist := &history.History{Events: []*history.HistoryEvent{
		{EventId: 1, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED},
	}}
	data, err := temporal.EncodeHistorySnapshot("flow_abc", hist)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "snap.pb")
	require.NoError(t, os.WriteFile(path, data, 0644))

	workflowId, got, wireBytes, err := readSnapshot(path)
	require.NoError(t, err)
	assert.Equal(t, "flow_abc", workflowId)
	assert.Equal(t, len(data), wireBytes)
	require.Len(t, got.Events, 1)
}

func TestSummarize_AttributesBytesToActivitiesAndCodec(t *testing.T) {
	t.Parallel()

	hist := &history.History{Events: []*history.HistoryEvent{
		{EventId: 1, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, Attributes: &history.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &history.ActivityTaskScheduledEventAttributes{
				ActivityType: &commonpb.ActivityType{Name: "BigResult"},
				Input:        inlinePayload(10),
			},
		}},
		{EventId: 2, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED, Attributes: &history.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &history.ActivityTaskCompletedEventAttributes{
				ScheduledEventId: 1,
				Result:           inlinePayload(5000),
			},
		}},
		{EventId: 3, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, Attributes: &history.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &history.ActivityTaskScheduledEventAttributes{
				ActivityType: &commonpb.ActivityType{Name: "Offloaded"},
				Input:        offloadedPayload(),
			},
		}},
		{EventId: 4, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED, Attributes: &history.HistoryEvent_WorkflowTaskScheduledEventAttributes{
			WorkflowTaskScheduledEventAttributes: &history.WorkflowTaskScheduledEventAttributes{},
		}},
	}}

	var out bytes.Buffer
	summarize(hist, 12345, 10, &out)
	got := out.String()

	assert.Contains(t, got, "events=4 wireBytes=12345")
	assert.Contains(t, got, "inline count=2")
	assert.Contains(t, got, "codec-offloaded count=1")

	lines := strings.Split(got, "\n")
	var splitLine string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "BigResult") && strings.Contains(line, "input=") {
			splitLine = line
		}
	}
	require.NotEmpty(t, splitLine, "expected an input/result split row for BigResult in:\n%s", got)
	assert.Contains(t, splitLine, "count=1")
	assert.Regexp(t, `input=\d{2}\s`, splitLine, "input should be tens of bytes")
	assert.Regexp(t, `result=50\d\d\s`, splitLine, "result should be ~5000 bytes")

	// The largest single event must be the completion carrying the 5000 byte result.
	largestIdx := strings.Index(got, "Largest individual events:")
	require.Greater(t, largestIdx, 0)
	firstLargest := strings.TrimSpace(strings.SplitN(got[largestIdx:], "\n", 3)[1])
	assert.True(t, strings.HasPrefix(firstLargest, "2 "), "largest event should be event 2, got %q", firstLargest)
	assert.Contains(t, firstLargest, "ActivityTaskCompleted BigResult")
}
