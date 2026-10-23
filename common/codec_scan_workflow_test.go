package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestPagedCodecCleanupWorkflow(t *testing.T) {
	for _, failMark := range []bool{false, true} {
		name := "all pages"
		if failMark {
			name = "mark failure prevents sweeping"
		}
		t.Run(name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			a := &CodecCleanupActivities{}
			env.RegisterActivity(a)
			env.OnActivity(a.BeginCodecScan, mock.Anything, mock.Anything).
				Return(CodecScanPageResult{}, nil).Once()
			if failMark {
				env.OnActivity(a.MarkCodecScanPage, mock.Anything, mock.Anything).
					Return(CodecScanPageResult{}, errors.New("unreadable history"))
			} else {
				env.OnActivity(a.MarkCodecScanPage, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return len(in.Token) == 0
				})).Return(CodecScanPageResult{NextToken: []byte("second-page"), Executions: 50}, nil).Once()
				env.OnActivity(a.MarkCodecScanPage, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return string(in.Token) == "second-page"
				})).Return(CodecScanPageResult{Done: true, Executions: 2}, nil).Once()
				env.OnActivity(a.SealCodecScan, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return in.Executions == 52
				})).Return(CodecScanPageResult{Done: true}, nil).Once()
				env.OnActivity(a.SweepCodecScanPage, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return in.Cursor == ""
				})).Return(CodecScanPageResult{NextCursor: "next", Candidates: 200, Bytes: 1000}, nil).Once()
				env.OnActivity(a.SweepCodecScanPage, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return in.Cursor == "next"
				})).Return(CodecScanPageResult{Done: true, Candidates: 10, Bytes: 100}, nil).Once()
			}
			env.OnActivity(a.DropCodecScanPage, mock.Anything, mock.Anything).
				Return(CodecScanPageResult{Done: true}, nil).Once()
			env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, CodecCleanupWorkflowInput{RunOnce: true, DryRun: true})
			require.True(t, env.IsWorkflowCompleted())
			if failMark {
				require.ErrorContains(t, env.GetWorkflowError(), "unreadable history")
				env.AssertNotCalled(t, "SealCodecScan", mock.Anything, mock.Anything)
				env.AssertNotCalled(t, "SweepCodecScanPage", mock.Anything, mock.Anything)
			} else {
				require.NoError(t, env.GetWorkflowError())
			}
			value, err := env.QueryWorkflow("scan")
			require.NoError(t, err)
			var summary CodecScanWorkflowState
			require.NoError(t, value.Get(&summary))
			require.Equal(t, "done", summary.Phase)
			if failMark {
				require.Contains(t, summary.Error, "unreadable history")
			} else {
				require.EqualValues(t, 52, summary.Executions)
				require.EqualValues(t, 210, summary.Candidates)
				require.EqualValues(t, 1100, summary.Bytes)
				require.Empty(t, summary.Error)
			}
			env.AssertExpectations(t)
		})
	}
}

func TestPagedCodecCleanupContinuesAndResumes(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"mark", "sweep", "drop"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			a := &CodecCleanupActivities{}
			env.RegisterActivity(a)
			startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			input := CodecCleanupWorkflowInput{
				RunOnce: true, DryRun: true, Retention: 7 * 24 * time.Hour,
				Scan: &CodecScanWorkflowState{
					ScanID: "original-scan", StartedAt: startedAt, Phase: phase,
					Token: []byte("page-0"), Cursor: "page-0",
					Executions: 10, Candidates: 20, Bytes: 30, SkippedOversize: 2,
				},
			}
			var pageActivity any
			switch phase {
			case "mark":
				pageActivity = a.MarkCodecScanPage
			case "sweep":
				pageActivity = a.SweepCodecScanPage
			case "drop":
				pageActivity = a.DropCodecScanPage
			}
			pages := 0
			env.OnActivity(pageActivity, mock.Anything, mock.Anything).
				Return(func(_ context.Context, in CodecScanPageInput) (CodecScanPageResult, error) {
					require.Equal(t, "original-scan", in.ScanID)
					expected := fmt.Sprintf("page-%d", pages)
					if phase == "mark" {
						require.Equal(t, expected, string(in.Token))
						require.Equal(t, int64(10+pages), in.Executions)
					} else {
						require.Equal(t, expected, in.Cursor)
					}
					pages++
					next := fmt.Sprintf("page-%d", pages)
					return CodecScanPageResult{
						NextToken: []byte(next), NextCursor: next,
						Executions: 1, Candidates: 2, Bytes: 3, SkippedOversize: 1,
					}, nil
				}).Times(200)
			env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)
			var continued *workflow.ContinueAsNewError
			require.ErrorAs(t, env.GetWorkflowError(), &continued)
			require.Equal(t, 200, pages)
			env.AssertExpectations(t)
			env.AssertNotCalled(t, "BeginCodecScan", mock.Anything, mock.Anything)
			if phase != "drop" {
				env.AssertNotCalled(t, "DropCodecScanPage", mock.Anything, mock.Anything)
			}
			var carried CodecCleanupWorkflowInput
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(continued.Input, &carried))
			require.NotNil(t, carried.Scan)
			for _, payload := range continued.Input.Payloads {
				require.Less(t, len(payload.Data), 4096, "continuation must carry cursors, not reference sets")
			}
			require.Equal(t, "original-scan", carried.Scan.ScanID)
			require.Equal(t, startedAt, carried.Scan.StartedAt)
			require.Equal(t, phase, carried.Scan.Phase)
			require.True(t, carried.RunOnce)
			require.True(t, carried.DryRun)
			require.Equal(t, input.Retention, carried.Retention)
			expected := *input.Scan
			switch phase {
			case "mark":
				expected.Token = []byte("page-200")
				expected.Executions += 200
			case "sweep":
				expected.Cursor = "page-200"
				expected.Candidates += 400
				expected.Bytes += 600
				expected.SkippedOversize += 200
			case "drop":
				expected.Cursor = "page-200"
			}
			require.Equal(t, expected, *carried.Scan)

			resumed := suite.NewTestWorkflowEnvironment()
			resumed.RegisterActivity(a)
			resumed.OnActivity(pageActivity, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
				return in.ScanID == carried.Scan.ScanID &&
					string(in.Token) == string(carried.Scan.Token) &&
					in.Cursor == carried.Scan.Cursor && in.Executions == carried.Scan.Executions
			})).Return(CodecScanPageResult{Done: true}, nil).Once()
			if phase == "mark" {
				resumed.OnActivity(a.SealCodecScan, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return in.Executions == 210
				})).Return(CodecScanPageResult{Done: true}, nil).Once()
				resumed.OnActivity(a.SweepCodecScanPage, mock.Anything, mock.Anything).
					Return(CodecScanPageResult{Done: true}, nil).Once()
			}
			if phase != "drop" {
				resumed.OnActivity(a.DropCodecScanPage, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
					return in.ScanID == "original-scan" && in.Cursor == ""
				})).Return(CodecScanPageResult{Done: true}, nil).Once()
			}
			resumed.ExecuteWorkflow(CodecPayloadCleanupWorkflow, carried)
			require.NoError(t, resumed.GetWorkflowError())
			resumed.AssertExpectations(t)
			resumed.AssertNotCalled(t, "BeginCodecScan", mock.Anything, mock.Anything)
			if phase != "mark" {
				resumed.AssertNotCalled(t, "MarkCodecScanPage", mock.Anything, mock.Anything)
			}
		})
	}
}

func TestPagedCodecCleanupWeeklyScheduleIgnoresLegacyDeletionSignals(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	a := &CodecCleanupActivities{}
	env.RegisterActivity(a)
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	env.SetStartTime(startedAt)
	dueAt := startedAt.Add(codecOrphanScanInterval)
	env.OnActivity(a.BeginCodecScan, mock.Anything, mock.MatchedBy(func(in CodecScanInput) bool {
		return in.StartedAt.Equal(dueAt) && in.Retention == DefaultCodecCleanupRetention && in.DryRun
	})).Return(CodecScanPageResult{}, nil).Once()
	env.OnActivity(a.MarkCodecScanPage, mock.Anything, mock.Anything).
		Return(CodecScanPageResult{Done: true, Executions: 1}, nil).Once()
	env.OnActivity(a.SealCodecScan, mock.Anything, mock.MatchedBy(func(in CodecScanPageInput) bool {
		return in.Executions == 1
	})).Return(CodecScanPageResult{Done: true}, nil).Once()
	env.OnActivity(a.SweepCodecScanPage, mock.Anything, mock.Anything).
		Return(CodecScanPageResult{Done: true}, nil).Once()
	env.OnActivity(a.DropCodecScanPage, mock.Anything, mock.Anything).
		Return(CodecScanPageResult{Done: true}, nil).Once()
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(codecCleanupSignalName, CodecCleanupSignal{WorkflowID: "finished", RunID: "old-run"})
		env.SignalWorkflow(codecCleanupResetSignalName, CodecCleanupResetSignal{WorkflowID: "finished"})
	}, time.Hour)
	env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, CodecCleanupWorkflowInput{
		LastOrphanScanAt: startedAt,
		DryRun:           true,
		Pending: []PendingDeletion{{
			WorkflowID: "finished", Keys: []string{"codec/still-referenced"},
			CollectedAt: startedAt.Add(-30 * 24 * time.Hour),
		}},
	})
	var continued *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &continued)
	var carried CodecCleanupWorkflowInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(continued.Input, &carried))
	require.True(t, carried.LastOrphanScanAt.Equal(dueAt))
	require.Empty(t, carried.Pending)
	require.Nil(t, carried.Scan)
	require.True(t, carried.DryRun)
	require.False(t, carried.RunOnce)
	env.AssertExpectations(t)
	env.AssertNotCalled(t, "DeleteCodecKeys", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CollectCodecKeysFromHistory", mock.Anything, mock.Anything, mock.Anything)
}
