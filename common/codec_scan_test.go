package common_test

import (
	"context"
	"errors"
	"sidekick/common"
	"sidekick/srv/sqlite"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"
)

func TestCodecScanPreservesReferencesAndBoundsPages(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		name := "delete"
		if dryRun {
			name = "dry run"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := sqlite.NewTestSqliteStorage(t, "codec_scan")
			t.Cleanup(func() { _ = store.Close() })
			now := time.Now().UTC()
			newKey := func(at time.Time) string {
				id, err := ksuid.NewRandomWithTime(at)
				require.NoError(t, err)
				return common.CodecKeyPrefix + id.String()
			}
			old := now.Add(-30 * 24 * time.Hour)
			referenced, recent, huge := newKey(old), newKey(now), newKey(old)
			values := map[string][]byte{
				referenced:             {1},
				recent:                 {2},
				huge:                   make([]byte, 32*1024*1024+1),
				"codec/unknown-format": {3},
			}
			var orphans []string
			for i := 0; i < 205; i++ {
				key := newKey(old)
				orphans = append(orphans, key)
				values[key] = []byte{4, 5}
			}
			require.NoError(t, store.MSetRaw(ctx, common.CodecWorkspaceID, values))

			c := &mocks.Client{}
			c.On("CountWorkflow", mock.Anything, mock.Anything).Return(
				&workflowservice.CountWorkflowExecutionsResponse{Count: 1}, nil)
			c.On("ListWorkflow", mock.Anything, mock.Anything).Return(
				&workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "wf", RunId: "run"}},
				}}, nil)
			iter := &mocks.HistoryEventIterator{}
			iter.On("HasNext").Return(true).Once()
			iter.On("HasNext").Return(false).Once()
			iter.On("Next").Return(&historypb.HistoryEvent{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
					WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
						Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{
							"ref": {Metadata: map[string][]byte{common.CodecMetadataKey: []byte(referenced)}},
						}},
					},
				},
			}, nil).Once()
			c.On("GetWorkflowHistory", mock.Anything, "wf", "run", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(iter)
			a := &common.CodecCleanupActivities{Storage: store, TemporalClient: c}
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(a)
			input := common.CodecScanInput{ScanID: "test-scan", StartedAt: now, Retention: common.DefaultCodecCleanupRetention, DryRun: dryRun}
			_, err := env.ExecuteActivity(a.BeginCodecScan, input)
			require.NoError(t, err)

			_, err = env.ExecuteActivity(a.SweepCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
			require.Error(t, err, "unsealed scans must never delete")
			v, err := env.ExecuteActivity(a.MarkCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
			require.NoError(t, err)
			var marked common.CodecScanPageResult
			require.NoError(t, v.Get(&marked))
			require.True(t, marked.Done)
			_, err = env.ExecuteActivity(a.SealCodecScan, common.CodecScanPageInput{ScanID: input.ScanID, Executions: marked.Executions})
			require.NoError(t, err)

			cursor := ""
			var total, oversized int64
			for page := 0; ; page++ {
				require.Less(t, page, 10)
				v, err := env.ExecuteActivity(a.SweepCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID, Cursor: cursor})
				require.NoError(t, err)
				var result common.CodecScanPageResult
				require.NoError(t, v.Get(&result))
				require.LessOrEqual(t, result.Candidates, int64(200))
				total += result.Candidates
				oversized += result.SkippedOversize
				if result.Done {
					break
				}
				cursor = result.NextCursor
			}
			require.EqualValues(t, 205, total)
			require.EqualValues(t, 1, oversized)
			kept, err := store.MGet(ctx, common.CodecWorkspaceID, []string{referenced, recent, huge, "codec/unknown-format"})
			require.NoError(t, err)
			for _, value := range kept {
				require.NotNil(t, value)
			}
			deleted, err := store.MGet(ctx, common.CodecWorkspaceID, orphans)
			require.NoError(t, err)
			for _, value := range deleted {
				if dryRun {
					require.NotNil(t, value)
				} else {
					require.Nil(t, value)
				}
			}
			c.AssertExpectations(t)
			iter.AssertExpectations(t)
		})
	}
}

func TestCodecScanFailsClosedOnHistoryAndCountErrors(t *testing.T) {
	store := sqlite.NewTestSqliteStorage(t, "codec_scan_errors")
	t.Cleanup(func() { _ = store.Close() })
	c := &mocks.Client{}
	c.On("CountWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.CountWorkflowExecutionsResponse{Count: 1}, nil).Once()
	c.On("CountWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.CountWorkflowExecutionsResponse{Count: 2}, nil).Once()
	c.On("ListWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{
			{Execution: &commonpb.WorkflowExecution{WorkflowId: "wf", RunId: "run"}},
		}}, nil)
	iter := &mocks.HistoryEventIterator{}
	iter.On("HasNext").Return(true)
	iter.On("Next").Return((*historypb.HistoryEvent)(nil), errors.New("history unavailable"))
	c.On("GetWorkflowHistory", mock.Anything, "wf", "run", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(iter)
	a := &common.CodecCleanupActivities{Storage: store, TemporalClient: c}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	input := common.CodecScanInput{ScanID: "failed-scan", StartedAt: time.Now(), Retention: common.DefaultCodecCleanupRetention}
	_, err := env.ExecuteActivity(a.BeginCodecScan, input)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(a.MarkCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
	require.ErrorContains(t, err, "history unavailable")
	_, err = env.ExecuteActivity(a.SealCodecScan, common.CodecScanPageInput{ScanID: input.ScanID, Executions: 1})
	require.Error(t, err)
	_, err = env.ExecuteActivity(a.SweepCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
	require.Error(t, err)
}

func TestCodecScanRejectsConflictingReuse(t *testing.T) {
	store := sqlite.NewTestSqliteStorage(t, "codec_scan_reuse")
	t.Cleanup(func() { _ = store.Close() })
	c := &mocks.Client{}
	c.On("CountWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.CountWorkflowExecutionsResponse{Count: 1}, nil).Once()
	a := &common.CodecCleanupActivities{Storage: store, TemporalClient: c}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	input := common.CodecScanInput{
		ScanID: "reused", StartedAt: time.Now().UTC(),
		Retention: common.DefaultCodecCleanupRetention,
	}
	_, err := env.ExecuteActivity(a.BeginCodecScan, input)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(a.BeginCodecScan, input)
	require.NoError(t, err, "identical retries must be accepted")
	for _, name := range []string{"dry run", "start time", "retention"} {
		t.Run(name, func(t *testing.T) {
			conflict := input
			switch name {
			case "dry run":
				conflict.DryRun = true
			case "start time":
				conflict.StartedAt = conflict.StartedAt.Add(time.Hour)
			case "retention":
				conflict.Retention += time.Hour
			}
			_, err := env.ExecuteActivity(a.BeginCodecScan, conflict)
			require.ErrorContains(t, err, "conflicting")
		})
	}
	c.AssertExpectations(t)
}

type observedCodecStorage struct {
	*sqlite.Storage
	batches [][]string
}

func (s *observedCodecStorage) MDelete(ctx context.Context, workspace string, keys []string) error {
	if workspace == common.CodecWorkspaceID {
		s.batches = append(s.batches, append([]string(nil), keys...))
	}
	return s.Storage.MDelete(ctx, workspace, keys)
}

func TestCodecScanSplitsAggregateByteBudget(t *testing.T) {
	ctx := context.Background()
	store := &observedCodecStorage{Storage: sqlite.NewTestSqliteStorage(t, "codec_scan_bytes")}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	values := map[string][]byte{}
	for i := 0; i < 4; i++ {
		id, err := ksuid.NewRandomWithTime(now.Add(-30 * 24 * time.Hour))
		require.NoError(t, err)
		values[common.CodecKeyPrefix+id.String()] = make([]byte, 10*1024*1024)
	}
	require.NoError(t, store.MSetRaw(ctx, common.CodecWorkspaceID, values))
	c := &mocks.Client{}
	c.On("CountWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.CountWorkflowExecutionsResponse{Count: 1}, nil)
	c.On("ListWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{
			{Execution: &commonpb.WorkflowExecution{WorkflowId: "wf", RunId: "run"}},
		}}, nil)
	iter := &mocks.HistoryEventIterator{}
	iter.On("HasNext").Return(false)
	c.On("GetWorkflowHistory", mock.Anything, "wf", "run", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).Return(iter)
	var recorded []common.KeySize
	a := &common.CodecCleanupActivities{
		Storage: store, TemporalClient: c,
		RecordCandidates: func(ctx context.Context, scanID string, keys []common.KeySize) error {
			require.Equal(t, "byte-budget", scanID)
			recorded = append(recorded, keys...)
			return nil
		},
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	input := common.CodecScanInput{ScanID: "byte-budget", StartedAt: now, Retention: common.DefaultCodecCleanupRetention}
	_, err := env.ExecuteActivity(a.BeginCodecScan, input)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(a.MarkCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
	require.NoError(t, err)
	_, err = env.ExecuteActivity(a.SealCodecScan, common.CodecScanPageInput{ScanID: input.ScanID, Executions: 1})
	require.NoError(t, err)
	recordCandidates := a.RecordCandidates
	a.RecordCandidates = func(context.Context, string, []common.KeySize) error {
		return errors.New("candidate not approved by audit")
	}
	_, err = env.ExecuteActivity(a.SweepCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
	require.ErrorContains(t, err, "candidate not approved by audit")
	require.Empty(t, store.batches)
	protected, err := store.GetKeysWithPrefix(ctx, common.CodecWorkspaceID, common.CodecKeyPrefix)
	require.NoError(t, err)
	require.Len(t, protected, 4)
	a.RecordCandidates = recordCandidates

	v, err := env.ExecuteActivity(a.SweepCodecScanPage, common.CodecScanPageInput{ScanID: input.ScanID})
	require.NoError(t, err)
	var result common.CodecScanPageResult
	require.NoError(t, v.Get(&result))
	require.EqualValues(t, 4, result.Candidates)
	require.EqualValues(t, 40*1024*1024, result.Bytes)
	require.Len(t, recorded, 4)
	for _, key := range recorded {
		require.EqualValues(t, len(values[key.Key]), key.Bytes)
	}
	require.Len(t, store.batches, 2)
	for _, batch := range store.batches {
		var bytes int
		for _, key := range batch {
			bytes += len(values[key])
		}
		require.LessOrEqual(t, bytes, 32*1024*1024)
	}
	keys, err := store.GetKeysWithPrefix(ctx, common.CodecWorkspaceID, common.CodecKeyPrefix)
	require.NoError(t, err)
	require.Empty(t, keys)
}
