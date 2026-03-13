package common

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestExtractPayloadsFromEvent_Scheduled(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
				Input: &commonpb.Payloads{
					Payloads: []*commonpb.Payload{
						{Metadata: map[string][]byte{codecMetadataKey: []byte("codec/abc")}, Data: nil},
						{Metadata: map[string][]byte{"encoding": []byte("json")}, Data: []byte("hello")},
					},
				},
			},
		},
	}
	payloads := extractPayloadsFromEvent(event)
	assert.Len(t, payloads, 2)
	assert.Equal(t, []byte("codec/abc"), payloads[0].Metadata[codecMetadataKey])
}

func TestExtractPayloadsFromEvent_WorkflowCompleted(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
			WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
				Result: &commonpb.Payloads{
					Payloads: []*commonpb.Payload{
						{Metadata: map[string][]byte{codecMetadataKey: []byte("codec/xyz")}},
					},
				},
			},
		},
	}
	payloads := extractPayloadsFromEvent(event)
	assert.Len(t, payloads, 1)
}

func TestExtractPayloadsFromEvent_EmptyEvent(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
	}
	payloads := extractPayloadsFromEvent(event)
	assert.Empty(t, payloads)
}

func TestExtractPayloadsFromEvent_MarkerRecorded(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_MARKER_RECORDED,
		EventTime: timestamppb.Now(),
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{
			MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
				Details: map[string]*commonpb.Payloads{
					"data": {
						Payloads: []*commonpb.Payload{
							{Metadata: map[string][]byte{codecMetadataKey: []byte("codec/marker1")}},
						},
					},
				},
			},
		},
	}
	payloads := extractPayloadsFromEvent(event)
	assert.Len(t, payloads, 1)
	assert.Equal(t, []byte("codec/marker1"), payloads[0].Metadata[codecMetadataKey])
}

func TestExtractPayloadsFromEvent_SignalInitiated(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED,
		Attributes: &historypb.HistoryEvent_SignalExternalWorkflowExecutionInitiatedEventAttributes{
			SignalExternalWorkflowExecutionInitiatedEventAttributes: &historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes{
				Input: &commonpb.Payloads{
					Payloads: []*commonpb.Payload{
						{Metadata: map[string][]byte{codecMetadataKey: []byte("codec/sig1")}},
					},
				},
			},
		},
	}
	payloads := extractPayloadsFromEvent(event)
	assert.Len(t, payloads, 1)
}

func TestOrphanDetection_KSUIDAge(t *testing.T) {
	t.Parallel()
	// KSUID from 10 days ago should be old enough for cleanup
	oldID, err := ksuid.NewRandomWithTime(time.Now().Add(-10 * 24 * time.Hour))
	assert.NoError(t, err)

	now := time.Now()
	age := now.Sub(oldID.Time())
	assert.True(t, age >= DefaultCodecCleanupRetention, "old KSUID should be past retention")

	// Recent KSUID should not be old enough
	recentID := ksuid.New()
	recentAge := now.Sub(recentID.Time())
	assert.True(t, recentAge < DefaultCodecCleanupRetention, "recent KSUID should not be past retention")
}

// --- Workflow-level tests using Temporal test suite ---

type CodecCleanupWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env        *testsuite.TestWorkflowEnvironment
	activities *CodecCleanupActivities
}

func (s *CodecCleanupWorkflowTestSuite) SetupTest() {
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(CodecPayloadCleanupWorkflow)
	s.activities = &CodecCleanupActivities{}
	s.env.RegisterActivity(s.activities.CollectCodecKeysFromHistory)
	s.env.RegisterActivity(s.activities.DeleteCodecKeys)
	s.env.RegisterActivity(s.activities.ListAllCodecKeys)
	s.env.RegisterActivity(s.activities.ListClosedWorkflowIDs)
	s.env.RegisterActivity(s.activities.ListRunningWorkflowCodecKeys)
}

func (s *CodecCleanupWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	if s.T().Failed() {
		s.T().FailNow()
	}
}

func TestCodecCleanupWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(CodecCleanupWorkflowTestSuite))
}

// TestKeysDeletedAfterRetention verifies that codec keys collected from a
// completed workflow are deleted after the retention period expires.
func (s *CodecCleanupWorkflowTestSuite) TestKeysDeletedAfterRetention() {
	retention := 2 * time.Hour

	// Pre-populate pending entries that are already past retention
	input := CodecCleanupWorkflowInput{
		Pending: []PendingDeletion{
			{
				WorkflowID:  "wf-old",
				Keys:        []string{"codec/old-key-1", "codec/old-key-2"},
				CollectedAt: time.Now().Add(-3 * time.Hour),
			},
		},
		Retention: retention,
	}

	// Expect deletion of the expired keys
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, []string{"codec/old-key-1", "codec/old-key-2"}).Return(nil).Once()

	// Orphan scan activities (triggered at ~7 day mark, but we'll hit ContinueAsNew first)
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	// Long-running workflow completes via ContinueAsNew
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}

// TestKeysNotDeletedBeforeRetention verifies that codec keys collected less
// than the retention period ago are NOT deleted.
func (s *CodecCleanupWorkflowTestSuite) TestKeysNotDeletedBeforeRetention() {
	input := CodecCleanupWorkflowInput{
		Pending: []PendingDeletion{
			{
				WorkflowID:  "wf-recent",
				Keys:        []string{"codec/recent-key"},
				CollectedAt: time.Now().Add(-1 * time.Hour),
			},
		},
		Retention: 10000 * time.Hour,
	}

	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()

	// Orphan scan activities
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}

// TestSignalCollectsKeysAndQueues verifies that receiving a signal triggers
// history collection and queues the keys for later deletion.
func (s *CodecCleanupWorkflowTestSuite) TestSignalCollectsKeysAndQueues() {
	input := CodecCleanupWorkflowInput{
		Retention: 10000 * time.Hour,
	}

	// When the signal is processed, expect history collection
	s.env.OnActivity(s.activities.CollectCodecKeysFromHistory, mock.Anything, "wf-123", "run-abc").
		Return([]string{"codec/collected-1", "codec/collected-2"}, nil).Once()

	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Orphan scan
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	// Send signal shortly after workflow starts
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(codecCleanupSignalName, CodecCleanupSignal{
			WorkflowID: "wf-123",
			RunID:      "run-abc",
		})
	}, 10*time.Millisecond)

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}

// TestDuplicateSignalReplacesExistingPending verifies that a signal for a
// workflow already in the pending list re-collects keys and replaces the entry.
func (s *CodecCleanupWorkflowTestSuite) TestDuplicateSignalReplacesExistingPending() {
	input := CodecCleanupWorkflowInput{
		Retention: 10000 * time.Hour,
		Pending: []PendingDeletion{
			{
				WorkflowID:  "wf-123",
				Keys:        []string{"codec/existing-key"},
				CollectedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}

	// Should re-collect keys since this could be a new run
	s.env.OnActivity(s.activities.CollectCodecKeysFromHistory, mock.Anything, "wf-123", "run-new").
		Return([]string{"codec/new-key-1", "codec/new-key-2"}, nil).Once()
	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()

	// Orphan scan
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	// Send signal for the already-pending workflow with a new run ID
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(codecCleanupSignalName, CodecCleanupSignal{
			WorkflowID: "wf-123",
			RunID:      "run-new",
		})
	}, 10*time.Millisecond)

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}

// TestOrphanScanDeletesUnreferencedOldKeys verifies that the weekly orphan scan
// deletes codec keys that are not referenced by any workflow and are old enough.
func (s *CodecCleanupWorkflowTestSuite) TestOrphanScanDeletesUnreferencedOldKeys() {
	// Create old KSUID keys (>7 days old)
	oldKsuid1, _ := ksuid.NewRandomWithTime(time.Now().Add(-10 * 24 * time.Hour))
	oldKsuid2, _ := ksuid.NewRandomWithTime(time.Now().Add(-8 * 24 * time.Hour))
	recentKsuid, _ := ksuid.NewRandomWithTime(time.Now().Add(-1 * time.Hour))
	referencedKsuid, _ := ksuid.NewRandomWithTime(time.Now().Add(-10 * 24 * time.Hour))

	oldKey1 := codecKeyPrefix + oldKsuid1.String()
	oldKey2 := codecKeyPrefix + oldKsuid2.String()
	recentKey := codecKeyPrefix + recentKsuid.String()
	referencedKey := codecKeyPrefix + referencedKsuid.String()

	// Set LastOrphanScanAt to trigger immediate orphan scan
	input := CodecCleanupWorkflowInput{
		LastOrphanScanAt: time.Now().Add(-8 * 24 * time.Hour),
		Retention:        DefaultCodecCleanupRetention,
	}

	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).
		Return([]string{oldKey1, oldKey2, recentKey, referencedKey}, nil).Maybe()

	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).
		Return([]CodecCleanupSignal{{WorkflowID: "wf-ref", RunID: "run-ref"}}, nil).Maybe()

	// The referenced workflow's history contains one of the old keys
	s.env.OnActivity(s.activities.CollectCodecKeysFromHistory, mock.Anything, "wf-ref", "run-ref").
		Return([]string{referencedKey}, nil).Maybe()

	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).
		Return([]string{}, nil).Maybe()

	// Only the unreferenced old keys should be deleted; recent key and referenced key are kept
	var deletedKeys []string
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			if deletedKeys == nil {
				deletedKeys = keys
			}
			return nil
		}).Maybe()

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Require().NotNil(deletedKeys, "DeleteCodecKeys should have been called")
	keySet := map[string]bool{}
	for _, k := range deletedKeys {
		keySet[k] = true
	}
	s.True(keySet[oldKey1], "old unreferenced key 1 should be deleted")
	s.True(keySet[oldKey2], "old unreferenced key 2 should be deleted")
	s.False(keySet[recentKey], "recent key should not be deleted")
	s.False(keySet[referencedKey], "referenced key should not be deleted")
	s.Len(deletedKeys, 2)
}

// TestEmptyWorkflowListAndHistoryHandledGracefully verifies the workflow handles
// empty lists of workflows and empty history without errors.
func (s *CodecCleanupWorkflowTestSuite) TestEmptyWorkflowListAndHistoryHandledGracefully() {
	input := CodecCleanupWorkflowInput{
		LastOrphanScanAt: time.Now().Add(-8 * 24 * time.Hour),
		Retention:        DefaultCodecCleanupRetention,
	}

	// All lists return empty
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}

// TestOrphanScanKeepsRunningWorkflowKeys verifies that codec keys referenced
// by running workflows are not deleted during orphan scan.
func (s *CodecCleanupWorkflowTestSuite) TestOrphanScanKeepsRunningWorkflowKeys() {
	oldKsuid, _ := ksuid.NewRandomWithTime(time.Now().Add(-10 * 24 * time.Hour))
	runningKey := codecKeyPrefix + oldKsuid.String()

	input := CodecCleanupWorkflowInput{
		LastOrphanScanAt: time.Now().Add(-8 * 24 * time.Hour),
		Retention:        DefaultCodecCleanupRetention,
	}

	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).
		Return([]string{runningKey}, nil).Maybe()

	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).
		Return([]CodecCleanupSignal{}, nil).Maybe()

	// The running workflow references this key
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).
		Return([]string{runningKey}, nil).Maybe()

	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
}

// TestCleanupWorkflowSkipsSelf verifies that a signal for the cleanup workflow
// itself is ignored and does not trigger history collection.
func (s *CodecCleanupWorkflowTestSuite) TestCleanupWorkflowSkipsSelf() {
	input := CodecCleanupWorkflowInput{
		Retention: 10000 * time.Hour,
	}

	var collectCalled atomic.Int32
	s.env.OnActivity(s.activities.CollectCodecKeysFromHistory, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, workflowID, runID string) ([]string, error) {
			collectCalled.Add(1)
			return nil, nil
		}).Maybe()
	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(codecCleanupSignalName, CodecCleanupSignal{
			WorkflowID: CodecCleanupWorkflowID,
			RunID:      "run-self",
		})
	}, 10*time.Millisecond)

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), collectCalled.Load(), "CollectCodecKeysFromHistory should not have been called")
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
}

// TestResetSignalRemovesPendingEntries verifies that a reset signal removes
// all pending entries for the given workflow ID.
func (s *CodecCleanupWorkflowTestSuite) TestResetSignalRemovesPendingEntries() {
	input := CodecCleanupWorkflowInput{
		Retention: 10000 * time.Hour,
		Pending: []PendingDeletion{
			{
				WorkflowID:  "wf-reset",
				Keys:        []string{"codec/old-run-key"},
				CollectedAt: time.Now().Add(-2 * time.Hour),
			},
			{
				WorkflowID:  "wf-keep",
				Keys:        []string{"codec/keep-key"},
				CollectedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}

	var collectCalled atomic.Int32
	s.env.OnActivity(s.activities.CollectCodecKeysFromHistory, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, workflowID, runID string) ([]string, error) {
			collectCalled.Add(1)
			return nil, nil
		}).Maybe()
	var deleteCalled atomic.Int32
	s.env.OnActivity(s.activities.DeleteCodecKeys, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, keys []string) error {
			deleteCalled.Add(1)
			return nil
		}).Maybe()
	s.env.OnActivity(s.activities.ListAllCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListClosedWorkflowIDs, mock.Anything).Return([]CodecCleanupSignal{}, nil).Maybe()
	s.env.OnActivity(s.activities.ListRunningWorkflowCodecKeys, mock.Anything).Return([]string{}, nil).Maybe()

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(codecCleanupResetSignalName, CodecCleanupResetSignal{
			WorkflowID: "wf-reset",
		})
	}, 10*time.Millisecond)

	s.env.ExecuteWorkflow(CodecPayloadCleanupWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Equal(int32(0), collectCalled.Load(), "CollectCodecKeysFromHistory should not have been called")
	s.Equal(int32(0), deleteCalled.Load(), "DeleteCodecKeys should not have been called")
	err := s.env.GetWorkflowError()
	s.NotNil(err)
	var continueAsNewErr *workflow.ContinueAsNewError
	s.ErrorAs(err, &continueAsNewErr)
}
