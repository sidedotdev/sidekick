package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ksuidAt(t *testing.T, at time.Time) string {
	t.Helper()
	id, err := ksuid.NewRandomWithTime(at)
	require.NoError(t, err)
	return id.String()
}

func openTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(schema)
	require.NoError(t, err)
	return db
}

type categoryTotal struct {
	Items int64
	Bytes int64
	Days  map[string]bool
}

func totalsByCategory(c *collector, source string) map[string]categoryTotal {
	totals := map[string]categoryTotal{}
	for key, stats := range c.buckets {
		if key.Source != source {
			continue
		}
		total := totals[key.Category]
		if total.Days == nil {
			total.Days = map[string]bool{}
		}
		total.Items += stats.Items
		total.Bytes += stats.Bytes
		total.Days[key.Day] = true
		totals[key.Category] = total
	}
	return totals
}

func TestNormalizeKey(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	k1, k2 := ksuidAt(t, at), ksuidAt(t, at)
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"ksuids", "flow_" + k1 + ":msg:block_" + k2, "flow_*:msg:block_*"},
		{"codec", "codec/" + k1, "codec/*"},
		{"uuid", "branch:550e8400-e29b-41d4-a716-446655440000", "branch:#"},
		{"hex", "diff:3f2a9c1be4d5a6f7", "diff:#"},
		{"base64 hash", "embedding:text-embedding-3-small:dir:chunk:h+5VBKPiWsVYs0NyTJBfL3lJ6M7D2SucQwC7kir6Fk8=", "embedding:text-embedding-#-small:dir:chunk:#"},
		{"number", "step:12", "step:#"},
		{"plain", "settings", "settings"},
		{"long word", "abcdefghij", "abcdefghij"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, normalizeKey(tt.key))
		})
	}
}

func TestKeyTimeUsesLastKSUID(t *testing.T) {
	t.Parallel()
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, newer, keyTime("flow_"+ksuidAt(t, older)+":msg:block_"+ksuidAt(t, newer)))
	assert.True(t, keyTime("settings").IsZero())
	future := ksuidAt(t, time.Now().AddDate(0, 2, 0))
	assert.True(t, keyTime("dir:chunk:"+future).IsZero())
	assert.True(t, keyTime("dir:chunk:"+future+"Zz+0=").IsZero())
}

func TestCollectorBoundsCategoriesPerSource(t *testing.T) {
	t.Parallel()
	c := newCollector(1<<20, 2)
	for _, category := range []string{"a", "b", "c", "a"} {
		c.add(item{Source: "s", Category: category, Bytes: 1})
	}
	c.add(item{Source: "other", Category: "c", Bytes: 1})

	totals := totalsByCategory(c, "s")
	assert.Equal(t, int64(2), totals["a"].Items)
	assert.Equal(t, int64(1), totals["b"].Items)
	assert.Equal(t, int64(1), totals[otherCategory].Items)
	assert.Equal(t, int64(1), c.overflowed["s"])
	assert.Equal(t, int64(1), totalsByCategory(c, "other")["c"].Items)
}

func TestScanKVAggregatesAcrossBatches(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, `CREATE TABLE kv (workspace_id TEXT NOT NULL, key TEXT NOT NULL, value BLOB, PRIMARY KEY (workspace_id, key))`)
	day := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	workspace := "ws_" + ksuidAt(t, day)
	flow := ksuidAt(t, day)
	codecKey := "codec/" + ksuidAt(t, day)
	rows := []struct {
		workspace, key string
		value          []byte
	}{
		{workspace, "flow_" + flow + ":msg:block_" + ksuidAt(t, day), bytes.Repeat([]byte("a"), 100)},
		{workspace, "flow_" + flow + ":msg:block_" + ksuidAt(t, day), bytes.Repeat([]byte("b"), 200)},
		{"__temporal_codec", codecKey, bytes.Repeat([]byte("c"), 2000)},
		{workspace, "settings", nil},
	}
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO kv (workspace_id, key, value) VALUES (?, ?, ?)`, r.workspace, r.key, r.value)
		require.NoError(t, err)
	}

	c := newCollector(1000, 100)
	codecEntries, err := scanKV(context.Background(), db, c, 2)
	require.NoError(t, err)
	assert.Equal(t, []codecEntry{{Key: codecKey, At: day.Truncate(time.Second), Bytes: 2000}}, codecEntries)

	totals := totalsByCategory(c, sourceKVEntries)
	blocks := totals["ws_* flow_*:msg:block_*"]
	assert.Equal(t, int64(2), blocks.Items)
	assert.Equal(t, int64(300), blocks.Bytes)
	assert.Equal(t, map[string]bool{"2026-09-20": true}, blocks.Days)
	assert.Equal(t, int64(2000), totals["__temporal_codec codec/*"].Bytes)
	assert.Equal(t, map[string]bool{"": true}, totals["ws_* settings"].Days)

	require.Len(t, c.large, 1)
	assert.Equal(t, codecKey, c.large[0].ID)
	assert.Equal(t, "__temporal_codec", c.large[0].Owner)
}

func TestScanFlowActionsMeasuresParamsAndResults(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, `CREATE TABLE flow_actions (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, flow_id TEXT NOT NULL, action_type TEXT NOT NULL,
		created DATETIME NOT NULL, action_params TEXT NOT NULL, action_result TEXT)`)
	created := time.Date(2026, 9, 21, 8, 30, 0, 0, time.UTC)
	results := []any{"ok", nil, string(bytes.Repeat([]byte("é"), 600))}
	for i, result := range results {
		_, err := db.Exec(`INSERT INTO flow_actions VALUES (?, 'ws', 'flow', 'tool_call.run_command', ?, '{"a":1}', ?)`,
			"fa"+string(rune('a'+i)), created, result)
		require.NoError(t, err)
	}

	c := newCollector(1000, 100)
	require.NoError(t, scanFlowActions(context.Background(), db, c, 2))

	params := totalsByCategory(c, sourceFlowActionParams)["tool_call.run_command"]
	assert.Equal(t, int64(3), params.Items)
	assert.Equal(t, int64(21), params.Bytes)
	assert.Equal(t, map[string]bool{"2026-09-21": true}, params.Days)
	resultTotals := totalsByCategory(c, sourceFlowActionResult)["tool_call.run_command"]
	assert.Equal(t, int64(2+1200), resultTotals.Bytes)
	require.Len(t, c.large, 1)
	assert.Equal(t, "fac", c.large[0].ID)
}

func payloadsOf(size int) *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: bytes.Repeat([]byte("x"), size)}}}
}

func historyBlob(t *testing.T, events ...*historypb.HistoryEvent) []byte {
	t.Helper()
	data, err := proto.Marshal(&historypb.History{Events: events})
	require.NoError(t, err)
	return data
}

func TestScanHistoryAttributesEvents(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, `CREATE TABLE history_node (
		shard_id INT NOT NULL, tree_id BINARY(16) NOT NULL, branch_id BINARY(16) NOT NULL,
		node_id BIGINT NOT NULL, txn_id BIGINT NOT NULL, prev_txn_id BIGINT NOT NULL DEFAULT 0,
		data MEDIUMBLOB NOT NULL, data_encoding VARCHAR(16) NOT NULL,
		PRIMARY KEY (shard_id, tree_id, branch_id, node_id, txn_id))`)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	runA, runB := uuid.New(), uuid.New()
	branch := uuid.New()

	started := func(id int64, workflowID string) *historypb.HistoryEvent {
		return &historypb.HistoryEvent{
			EventId:   id,
			EventTime: timestamppb.New(at),
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "DevWorkflow"},
					WorkflowId:   workflowID,
				},
			},
		}
	}
	scheduled := &historypb.HistoryEvent{
		EventId:   2,
		EventTime: timestamppb.New(at),
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
				ActivityType: &commonpb.ActivityType{Name: "EnvRunCommandActivity"},
				Input:        payloadsOf(100),
			},
		},
	}
	activityStarted := &historypb.HistoryEvent{
		EventId:   3,
		EventTime: timestamppb.New(at),
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED,
		Attributes: &historypb.HistoryEvent_ActivityTaskStartedEventAttributes{
			ActivityTaskStartedEventAttributes: &historypb.ActivityTaskStartedEventAttributes{ScheduledEventId: 2},
		},
	}
	completed := &historypb.HistoryEvent{
		EventId:   4,
		EventTime: timestamppb.New(at),
		EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
				ScheduledEventId: 2,
				Result:           payloadsOf(5000),
			},
		},
	}
	marker := &historypb.HistoryEvent{
		EventId:   2,
		EventTime: timestamppb.New(at),
		EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				MarkerName: "SideEffect",
				Details:    map[string]*commonpb.Payloads{"data": payloadsOf(5000)},
			},
		},
	}
	codecKey := "codec/" + ksuidAt(t, at)
	offloadedSignal := &historypb.HistoryEvent{
		EventId:   3,
		EventTime: timestamppb.New(at),
		EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionSignaledEventAttributes{
			WorkflowExecutionSignaledEventAttributes: &historypb.WorkflowExecutionSignaledEventAttributes{
				SignalName: "userAction",
				Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{{
					Metadata: map[string][]byte{"sidekick-payload-codec-key": []byte(codecKey)},
				}}},
			},
		},
	}

	// The later node of run A is inserted first: attribution must follow
	// history order, not insertion order.
	insert := func(tree uuid.UUID, nodeID int64, data []byte) {
		_, err := db.Exec(`INSERT INTO history_node (shard_id, tree_id, branch_id, node_id, txn_id, data, data_encoding)
			VALUES (1, ?, ?, ?, 1, ?, 'Proto3')`, tree[:], branch[:], nodeID, data)
		require.NoError(t, err)
	}
	insert(runA, 3, historyBlob(t, activityStarted, completed))
	insert(runA, 1, historyBlob(t, started(1, "wf-a"), scheduled))
	insert(runB, 1, historyBlob(t, started(1, ""), marker, offloadedSignal))

	c := newCollector(4000, 100)
	refs, err := scanHistory(context.Background(), db, c, map[string]string{runB.String(): "wf-b"})
	require.NoError(t, err)
	assert.Equal(t, []codecRef{{Key: codecKey, RunID: runB.String()}}, refs)
	assert.Equal(t, int64(1), totalsByCategory(c, sourceTemporalEvent)[enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED.String()+":userAction"].Items)

	events := totalsByCategory(c, sourceTemporalEvent)
	activityType := ":EnvRunCommandActivity"
	assert.Equal(t, int64(1), events[enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED.String()+activityType].Items)
	assert.Equal(t, int64(1), events[enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED.String()+activityType].Items)
	assert.Equal(t, int64(1), events[enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED.String()+activityType].Items)
	assert.Equal(t, int64(2), events[enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED.String()+":DevWorkflow"].Items)
	assert.Equal(t, map[string]bool{"2026-09-22": true}, events[enumspb.EVENT_TYPE_MARKER_RECORDED.String()+":SideEffect"].Days)

	assert.Equal(t, int64(3), totalsByCategory(c, sourceTemporalBatch)["DevWorkflow"].Items)

	largeEventOwners := map[string]string{}
	for _, it := range c.large {
		if it.Source == sourceTemporalEvent {
			largeEventOwners[it.ID] = it.Owner
		}
	}
	assert.Equal(t, map[string]string{
		runA.String() + "#4": "wf-a",
		runB.String() + "#2": "wf-b",
	}, largeEventOwners)
}
