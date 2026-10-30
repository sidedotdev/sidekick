package main

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/temporal"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
)

func snapshotHistory() *history.History {
	return &history.History{Events: []*history.HistoryEvent{
		{EventId: 1, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED},
		{EventId: 2, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED},
	}}
}

func TestHistorySnapshot_RoundTrip(t *testing.T) {
	t.Parallel()
	data, err := temporal.EncodeHistorySnapshot("flow_abc", snapshotHistory())
	require.NoError(t, err)

	hist, err := temporal.DecodeHistorySnapshotFor("flow_abc", data)
	require.NoError(t, err)
	require.Len(t, hist.Events, 2)
	assert.Equal(t, int64(2), hist.Events[1].GetEventId())
}

func TestHistorySnapshot_RejectsOtherWorkflow(t *testing.T) {
	t.Parallel()
	data, err := temporal.EncodeHistorySnapshot("flow_abc", snapshotHistory())
	require.NoError(t, err)

	_, err = temporal.DecodeHistorySnapshotFor("flow_xyz", data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow_abc")
	assert.Contains(t, err.Error(), "flow_xyz")
}

func TestHistorySnapshot_RejectsCorruptData(t *testing.T) {
	t.Parallel()
	_, err := temporal.DecodeHistorySnapshotFor("flow_abc", []byte("no header here"))
	require.Error(t, err)

	_, err = temporal.DecodeHistorySnapshotFor("flow_abc", []byte("flow_abc\n\xff\xff\xff garbage"))
	require.Error(t, err)
}

// loadOrFetchHistory must not touch the Temporal client when the snapshot is
// valid; a nil client would panic if it did.
func TestLoadOrFetchHistory_SnapshotHitSkipsFetch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "hist.pb")
	data, err := temporal.EncodeHistorySnapshot("flow_abc", snapshotHistory())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0644))

	hist, err := loadOrFetchHistory(context.Background(), nil, "flow_abc", path)
	require.NoError(t, err)
	assert.Len(t, hist.Events, 2)
}

func TestLoadOrFetchHistory_CorruptSnapshotFailsLoudly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "hist.pb")
	require.NoError(t, os.WriteFile(path, []byte("flow_abc\n\xff\xff"), 0644))

	_, err := loadOrFetchHistory(context.Background(), nil, "flow_abc", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

func TestTimingDataConverter_CountsCallsAndErrors(t *testing.T) {
	t.Parallel()
	dc := newTimingDataConverter(converter.GetDefaultDataConverter())

	payloads, err := dc.ToPayloads("a", 2)
	require.NoError(t, err)
	var s string
	var n int
	require.NoError(t, dc.FromPayloads(payloads, &s, &n))
	assert.Equal(t, "a", s)
	assert.Equal(t, 2, n)

	var wrong struct{ X int }
	require.Error(t, dc.FromPayload(payloads.Payloads[0], &wrong))

	assert.Equal(t, int64(1), dc.to.calls)
	assert.Equal(t, int64(2), dc.from.calls)
	assert.Equal(t, int64(1), dc.from.errors)
	assert.Greater(t, dc.from.bytes, int64(0))

	assert.Equal(t, int64(1), dc.fromByType["string,int"].calls)
	assert.Equal(t, int64(1), dc.fromByType["struct { X int }"].errors)
	assert.Equal(t, int64(1), dc.toByType["string,int"].calls)
}
