package flow_action

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/secret_manager"
	"sidekick/temporalmeta"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type TrackTemporalActivitiesTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment

	mu                  sync.Mutex
	persistedActions    []domain.FlowAction
	headerFlowActionIds []string
}

func (s *TrackTemporalActivitiesTestSuite) SetupTest() {
	s.T().Helper()
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))
	s.SetContextPropagators([]workflow.ContextPropagator{NewFlowActionIdPropagator()})

	s.env = s.NewTestWorkflowEnvironment()
	s.persistedActions = nil
	s.headerFlowActionIds = nil

	var fa *FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(
		func(_ context.Context, flowAction domain.FlowAction) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.persistedActions = append(s.persistedActions, flowAction)
			return nil
		},
	)
}

func (s *TrackTemporalActivitiesTestSuite) getPersisted() []domain.FlowAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]domain.FlowAction, len(s.persistedActions))
	copy(cp, s.persistedActions)
	return cp
}

func (s *TrackTemporalActivitiesTestSuite) getHeaderFlowActionIds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]string, len(s.headerFlowActionIds))
	copy(cp, s.headerFlowActionIds)
	return cp
}

func (s *TrackTemporalActivitiesTestSuite) captureHeaderFromCtx(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := ctx.Value(flowActionIdCtxKey).(string); ok {
		s.headerFlowActionIds = append(s.headerFlowActionIds, val)
	}
}

func stubActivityA(_ context.Context, _ string) (string, error) {
	return "resultA", nil
}

func stubActivityB(_ context.Context, _ int) (int, error) {
	return 42, nil
}

// registerActivityStubsWithHeaderCapture registers OnActivity handlers for
// stubActivityA and stubActivityB that capture the propagated flow action ID
// from the activity context before returning their normal results.
func (s *TrackTemporalActivitiesTestSuite) registerActivityStubsWithHeaderCapture() {
	s.env.RegisterActivity(stubActivityA)
	s.env.RegisterActivity(stubActivityB)

	s.env.OnActivity(stubActivityA, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input string) (string, error) {
			s.captureHeaderFromCtx(ctx)
			return "resultA", nil
		},
	)
	s.env.OnActivity(stubActivityB, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input int) (int, error) {
			s.captureHeaderFromCtx(ctx)
			return 42, nil
		},
	)
}

func testActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})
}

func (s *TrackTemporalActivitiesTestSuite) newTestExecContext(ctx workflow.Context) ExecContext {
	return ExecContext{
		Context:     ctx,
		WorkspaceId: "ws_test",
		FlowScope: &FlowScope{
			SubflowName: "test-subflow",
		},
		Secrets: &secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.MockSecretManager{},
		},
		EnvContainer: &env.EnvContainer{},
		LLMConfig:    common.LLMConfig{},
		GlobalState:  &GlobalState{},
	}
}

// TestTrackDecoratesWithMultipleActivities runs a workflow that tracks a
// closure scheduling multiple activities (including parallel via workflow.Go),
// then verifies that the decoration persist contains TemporalActivityRefs for
// all of them with the correct flow action ID passed to FetchFlowActionActivities.
func (s *TrackTemporalActivitiesTestSuite) TestTrackDecoratesWithMultipleActivities() {
	expectedActivities := []domain.TemporalActivityRef{
		{ActivityType: "stubActivityA", ActivityId: "1", ScheduledEventId: 10, CloseEventId: 11, CloseEventType: "EVENT_TYPE_ACTIVITY_TASK_COMPLETED"},
		{ActivityType: "stubActivityB", ActivityId: "2", ScheduledEventId: 12, CloseEventId: 13, CloseEventType: "EVENT_TYPE_ACTIVITY_TASK_COMPLETED"},
		{ActivityType: "stubActivityA", ActivityId: "3", ScheduledEventId: 14, CloseEventId: 15, CloseEventType: "EVENT_TYPE_ACTIVITY_TASK_COMPLETED"},
	}

	var capturedFetchParams temporalmeta.FetchFlowActionActivitiesParams
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return(
		func(_ context.Context, params temporalmeta.FetchFlowActionActivitiesParams) ([]domain.TemporalActivityRef, error) {
			capturedFetchParams = params
			return expectedActivities, nil
		},
	)

	s.registerActivityStubsWithHeaderCapture()

	testWorkflow := func(ctx workflow.Context) error {
		ctx = testActivityOptions(ctx)
		eCtx := s.newTestExecContext(ctx)
		actionCtx := eCtx.NewActionContext("multi_activity_action")

		_, err := Track(actionCtx, func(fa *domain.FlowAction) (string, error) {
			// Sequential activity
			var resA string
			if err := workflow.ExecuteActivity(actionCtx, stubActivityA, "seq").Get(actionCtx, &resA); err != nil {
				return "", err
			}

			// Two parallel activities via workflow.Go
			errCh := workflow.NewChannel(actionCtx)
			workflow.Go(actionCtx, func(gCtx workflow.Context) {
				var resB int
				err := workflow.ExecuteActivity(gCtx, stubActivityB, 7).Get(gCtx, &resB)
				errCh.Send(gCtx, err)
			})
			workflow.Go(actionCtx, func(gCtx workflow.Context) {
				var resA2 string
				err := workflow.ExecuteActivity(gCtx, stubActivityA, "par").Get(gCtx, &resA2)
				errCh.Send(gCtx, err)
			})

			// Collect results from both parallel goroutines
			for i := 0; i < 2; i++ {
				var chErr error
				errCh.Receive(actionCtx, &chErr)
				if chErr != nil {
					return "", chErr
				}
			}

			return resA, nil
		})
		if err != nil {
			return err
		}
		_ = workflow.Sleep(ctx, time.Millisecond)
		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify FetchFlowActionActivities was called with the correct flow action ID
	s.True(strings.HasPrefix(capturedFetchParams.FlowActionId, "fa_"),
		"expected FlowActionId to start with 'fa_', got: %s", capturedFetchParams.FlowActionId)

	// Find the flow action ID from the initial persist
	persisted := s.getPersisted()
	s.GreaterOrEqual(len(persisted), 3, "expected at least 3 PersistFlowAction calls (started, complete, decoration)")

	// The first persist creates the flow action with a generated ID
	initialFlowActionId := persisted[0].Id
	s.True(strings.HasPrefix(initialFlowActionId, "fa_"))

	// FetchFlowActionActivities should have been called with that same ID
	s.Equal(initialFlowActionId, capturedFetchParams.FlowActionId,
		"FetchFlowActionActivities should receive the same flow action ID that was persisted")

	// Verify the sidekickFlowActionId header was injected into all 3 activities
	headerIds := s.getHeaderFlowActionIds()
	s.Len(headerIds, 3, "expected 3 activities to receive the flow action ID header")
	for i, hid := range headerIds {
		s.Equal(initialFlowActionId, hid,
			"activity %d should have received flow action ID %s in header, got %s", i, initialFlowActionId, hid)
	}

	// The decoration persist (last one) should carry the expected TemporalActivities
	lastPersist := persisted[len(persisted)-1]
	s.Equal(domain.ActionStatusComplete, lastPersist.ActionStatus)
	s.Equal(expectedActivities, lastPersist.TemporalActivities)
	s.Equal(initialFlowActionId, lastPersist.Id,
		"decoration persist should update the same flow action")
}

// TestTrackDecoratesOnFailure verifies that even when the tracked closure fails,
// the decoration goroutine still fetches and persists temporal activity refs.
func (s *TrackTemporalActivitiesTestSuite) TestTrackDecoratesOnFailure() {
	expectedActivities := []domain.TemporalActivityRef{
		{ActivityType: "stubActivityA", ActivityId: "1", ScheduledEventId: 5, CloseEventId: 6, CloseEventType: "EVENT_TYPE_ACTIVITY_TASK_FAILED"},
	}

	var capturedFetchParams temporalmeta.FetchFlowActionActivitiesParams
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return(
		func(_ context.Context, params temporalmeta.FetchFlowActionActivitiesParams) ([]domain.TemporalActivityRef, error) {
			capturedFetchParams = params
			return expectedActivities, nil
		},
	)

	s.registerActivityStubsWithHeaderCapture()

	testWorkflow := func(ctx workflow.Context) error {
		ctx = testActivityOptions(ctx)
		eCtx := s.newTestExecContext(ctx)
		actionCtx := eCtx.NewActionContext("failing_action")

		_, err := Track(actionCtx, func(fa *domain.FlowAction) (string, error) {
			// Execute an activity before failing so we can verify header injection
			var res string
			_ = workflow.ExecuteActivity(actionCtx, stubActivityA, "before-fail").Get(actionCtx, &res)
			return "", errors.New("simulated action failure")
		})
		_ = err
		_ = workflow.Sleep(ctx, time.Millisecond)
		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	persisted := s.getPersisted()

	// The initial persist and the failure persist should share the same ID
	initialFlowActionId := persisted[0].Id
	s.Equal(initialFlowActionId, capturedFetchParams.FlowActionId)

	// Verify the header was injected into the activity executed before failure
	headerIds := s.getHeaderFlowActionIds()
	s.Len(headerIds, 1, "expected 1 activity to receive the flow action ID header")
	if len(headerIds) > 0 {
		s.Equal(initialFlowActionId, headerIds[0])
	}

	var decorated *domain.FlowAction
	for i := len(persisted) - 1; i >= 0; i-- {
		if len(persisted[i].TemporalActivities) > 0 {
			decorated = &persisted[i]
			break
		}
	}
	s.NotNil(decorated, "expected a persist call with TemporalActivities")
	if decorated != nil {
		s.Equal(domain.ActionStatusFailed, decorated.ActionStatus)
		s.Equal(expectedActivities, decorated.TemporalActivities)
		s.Equal(initialFlowActionId, decorated.Id)
	}
}

// TestTrackNoDecorationPersistWhenFetchReturnsEmpty verifies that when
// FetchFlowActionActivities returns an empty slice, no extra decoration
// persist is issued.
func (s *TrackTemporalActivitiesTestSuite) TestTrackNoDecorationPersistWhenFetchReturnsEmpty() {
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return([]domain.TemporalActivityRef{}, nil)

	s.registerActivityStubsWithHeaderCapture()

	testWorkflow := func(ctx workflow.Context) error {
		ctx = testActivityOptions(ctx)
		eCtx := s.newTestExecContext(ctx)
		actionCtx := eCtx.NewActionContext("test_action")

		_, err := Track(actionCtx, func(fa *domain.FlowAction) (string, error) {
			return "done", nil
		})
		if err != nil {
			return err
		}
		_ = workflow.Sleep(ctx, time.Millisecond)
		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	persisted := s.getPersisted()
	// Only 2 persists expected: initial (started) + completion (complete).
	// No third decoration persist since fetch returned empty.
	s.Equal(2, len(persisted), "expected exactly 2 PersistFlowAction calls when no activities found")
	for _, p := range persisted {
		s.Empty(p.TemporalActivities)
	}

	// No activities were invoked inside the closure, so no headers should be captured
	s.Empty(s.getHeaderFlowActionIds())
}

// TestTrackFlowActionIdConsistency verifies that the flow action ID generated
// by Track is consistently used across all persists and passed to
// FetchFlowActionActivities, confirming end-to-end association.
func (s *TrackTemporalActivitiesTestSuite) TestTrackFlowActionIdConsistency() {
	var capturedParams temporalmeta.FetchFlowActionActivitiesParams
	captured := false

	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return(
		func(_ context.Context, params temporalmeta.FetchFlowActionActivitiesParams) ([]domain.TemporalActivityRef, error) {
			capturedParams = params
			captured = true
			return []domain.TemporalActivityRef{
				{ActivityType: "stubActivityA", ActivityId: "1", ScheduledEventId: 1, CloseEventId: 2, CloseEventType: "EVENT_TYPE_ACTIVITY_TASK_COMPLETED"},
			}, nil
		},
	)

	s.registerActivityStubsWithHeaderCapture()

	testWorkflow := func(ctx workflow.Context) error {
		ctx = testActivityOptions(ctx)
		eCtx := s.newTestExecContext(ctx)
		actionCtx := eCtx.NewActionContext("consistency_action")

		_, err := Track(actionCtx, func(fa *domain.FlowAction) (string, error) {
			var result string
			err := workflow.ExecuteActivity(actionCtx, stubActivityA, "input").Get(actionCtx, &result)
			return result, err
		})
		if err != nil {
			return err
		}
		_ = workflow.Sleep(ctx, time.Millisecond)
		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.True(captured, "FetchFlowActionActivities should have been called")

	persisted := s.getPersisted()
	s.GreaterOrEqual(len(persisted), 3)

	// All persists must share the same flow action ID
	flowActionId := persisted[0].Id
	s.True(strings.HasPrefix(flowActionId, "fa_"))
	for i, p := range persisted {
		s.Equal(flowActionId, p.Id, "persist %d should have the same flow action ID", i)
	}

	// FetchFlowActionActivities received that same ID
	s.Equal(flowActionId, capturedParams.FlowActionId)

	// The header injected into the activity must match the same flow action ID
	headerIds := s.getHeaderFlowActionIds()
	s.Len(headerIds, 1)
	if len(headerIds) > 0 {
		s.Equal(flowActionId, headerIds[0],
			"activity header flow action ID should match the persisted flow action ID")
	}

	// The decoration persist has the right status and activities
	lastPersist := persisted[len(persisted)-1]
	s.Equal(domain.ActionStatusComplete, lastPersist.ActionStatus)
	s.Len(lastPersist.TemporalActivities, 1)
}

func TestTrackTemporalActivitiesTestSuite(t *testing.T) {
	suite.Run(t, new(TrackTemporalActivitiesTestSuite))
}
