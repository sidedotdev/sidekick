package flow_action

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
)

const retryAttemptsChildWorkflowId = "retry-attempts-child"

type retryAttemptsParams struct {
	FailuresOnly bool
	// Failures is how many leading attempts fail before success.
	Failures int32
	// RateLimited makes the failures provider rate limits, which retry
	// automatically; otherwise the user answers the continue prompt.
	RateLimited bool
	// GiveUp makes the failure handler give up after the last failure
	// instead of retrying.
	GiveUp bool
	// CancelDuringWait cancels the workflow while the first automatic retry
	// is pending.
	CancelDuringWait bool
	// UserRespond makes the user hit Try Again on rate limit requests too,
	// instead of letting the automatic retry fire.
	UserRespond bool
}

// observeRetryAttemptsActivity lets the parent workflow snapshot storage while
// the child is waiting on a user request. Mocked per test.
func observeRetryAttemptsActivity(ctx context.Context) error {
	return errors.New("must be mocked")
}

func retryAttemptsChildWorkflow(ctx workflow.Context, params retryAttemptsParams) (string, error) {
	eCtx := newActivityRetryTestExecContext(ctx, false)
	actionCtx := eCtx.NewActionContext(activityRetryTestActionName)

	var inner ActivityFailureHandler = ContinuePromptFailureHandler{}
	if params.RateLimited {
		inner = &RateLimitFailureHandler{}
	}
	failures := int32(0)
	handler := ActivityFailureHandlerFunc(func(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
		failures++
		if params.GiveUp && failures == params.Failures {
			return RetryDecisionFail, nil
		}
		return inner.HandleActivityFailure(eCtx, actionName, activityErr)
	})

	perform := func(actionCtx ActionContext, _ *domain.FlowAction) (string, error) {
		var value string
		err := PerformWithUserRetryOptions(actionCtx, UserRetryOptions{OnFailure: handler}, activityRetryTestActivity, &value)
		return value, err
	}
	if params.FailuresOnly {
		return TrackFailureOnly(actionCtx, perform)
	}
	return Track(actionCtx, perform)
}

func retryAttemptsParentWorkflow(ctx workflow.Context, params retryAttemptsParams) (string, error) {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: retryAttemptsChildWorkflowId})
	childCtx, cancelChild := workflow.WithCancel(childCtx)

	requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		for {
			var req RequestForUser
			requests.Receive(ctx, &req)
			if err := workflow.ExecuteActivity(ctx, observeRetryAttemptsActivity).Get(ctx, nil); err != nil {
				panic(err)
			}
			if isRateLimit, _ := req.RequestParams["rateLimited"].(bool); isRateLimit && !params.UserRespond {
				if params.CancelDuringWait {
					cancelChild()
				}
				continue
			}
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
				FlowActionId: req.FlowActionId,
			}).Get(ctx, nil)
		}
	})

	var value string
	err := workflow.ExecuteChildWorkflow(childCtx, retryAttemptsChildWorkflow, params).Get(ctx, &value)
	return value, err
}

func TestRetryAttemptsAreRecordedOnTrackedAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params retryAttemptsParams
		// wantNoAction means no flow action row is expected at all.
		wantNoAction bool
		wantStatus   domain.ActionStatus
	}{
		{
			name:       "tracked action keeps attempts after success",
			params:     retryAttemptsParams{Failures: 2, RateLimited: true},
			wantStatus: domain.ActionStatusComplete,
		},
		{
			name:       "tracked action records rate limits retried early by the user",
			params:     retryAttemptsParams{Failures: 2, RateLimited: true, UserRespond: true},
			wantStatus: domain.ActionStatusComplete,
		},
		{
			name:       "tracked action records manually retried failures",
			params:     retryAttemptsParams{Failures: 1},
			wantStatus: domain.ActionStatusComplete,
		},
		{
			name:       "tracked action keeps attempts after giving up",
			params:     retryAttemptsParams{Failures: 2, GiveUp: true},
			wantStatus: domain.ActionStatusFailed,
		},
		{
			name:       "failures-only action is finalized after a retried success",
			params:     retryAttemptsParams{Failures: 1, RateLimited: true, FailuresOnly: true},
			wantStatus: domain.ActionStatusComplete,
		},
		{
			name:       "failures-only action keeps attempts after giving up",
			params:     retryAttemptsParams{Failures: 2, FailuresOnly: true, GiveUp: true},
			wantStatus: domain.ActionStatusFailed,
		},
		{
			name:         "failures-only action is not persisted when the first attempt succeeds",
			params:       retryAttemptsParams{FailuresOnly: true},
			wantNoAction: true,
		},
		{
			name:       "cancelling during an automatic wait keeps history and drops the schedule",
			params:     retryAttemptsParams{Failures: 2, RateLimited: true, CancelDuringWait: true},
			wantStatus: domain.ActionStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage := sqlite.NewTestSqliteStorage(t, "retry-attempts")
			service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
			require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
				WorkspaceId: "ws_test",
				Id:          retryAttemptsChildWorkflowId,
				Status:      "in_progress",
			}))

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			env.RegisterWorkflow(retryAttemptsChildWorkflow)
			env.RegisterWorkflow(retryAttemptsParentWorkflow)
			env.RegisterActivity(&FlowActivities{Service: service})
			env.RegisterActivity(&srv.Activities{Service: service})

			// pendingSnapshots holds the retried (non-human) flow actions in
			// storage as seen while each user request is pending, before the
			// user or the timer resolves it. attemptSnapshots holds them as
			// seen at the start of each attempt after the first.
			var mu sync.Mutex
			var calls int32
			var pendingSnapshots, attemptSnapshots [][]domain.FlowAction
			env.OnActivity(observeRetryAttemptsActivity, mock.Anything).Return(
				func(ctx context.Context) error {
					actions, err := storage.GetFlowActions(ctx, "ws_test", retryAttemptsChildWorkflowId)
					if err != nil {
						return err
					}
					mu.Lock()
					defer mu.Unlock()
					pendingSnapshots = append(pendingSnapshots, retriedActions(actions))
					return nil
				},
			)
			env.OnActivity(activityRetryTestActivity, mock.Anything).Return(
				func(ctx context.Context) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					calls++
					if calls > 1 {
						actions, err := storage.GetFlowActions(ctx, "ws_test", retryAttemptsChildWorkflowId)
						require.NoError(t, err)
						attemptSnapshots = append(attemptSnapshots, retriedActions(actions))
					}
					if calls <= tt.params.Failures {
						if tt.params.RateLimited {
							return "", common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{Provider: "anthropic"}, errors.New("429 Too Many Requests"))
						}
						return "", errors.New("simulated failure")
					}
					return "success", nil
				},
			)

			env.ExecuteWorkflow(retryAttemptsParentWorkflow, tt.params)
			require.True(t, env.IsWorkflowCompleted())
			if tt.wantStatus == domain.ActionStatusFailed {
				require.Error(t, env.GetWorkflowError())
			} else {
				require.NoError(t, env.GetWorkflowError())
			}

			actions, err := storage.GetFlowActions(context.Background(), "ws_test", retryAttemptsChildWorkflowId)
			require.NoError(t, err)
			retried := retriedActions(actions)
			if tt.wantNoAction {
				assert.Empty(t, retried)
				return
			}
			require.Len(t, retried, 1)
			final := retried[0]
			assert.Equal(t, tt.wantStatus, final.ActionStatus)
			assert.Nil(t, final.NextRetryAt)
			wantAttempts := int(tt.params.Failures)
			if tt.params.CancelDuringWait {
				wantAttempts = 1
			}
			require.Len(t, final.Attempts, wantAttempts)
			for i, attempt := range final.Attempts {
				if tt.params.RateLimited {
					assert.Contains(t, attempt.Error, "429 Too Many Requests")
				} else {
					assert.Contains(t, attempt.Error, "simulated failure")
				}
				if i > 0 {
					assert.False(t, attempt.At.Before(final.Attempts[i-1].At), "attempts must be chronological")
				}
			}

			// While each request is pending, the failures so far must already
			// be recorded on the same action, with the automatic retry
			// scheduled only for rate limits.
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, pendingSnapshots, wantAttempts-boolToInt(tt.params.GiveUp))
			for i, snapshot := range pendingSnapshots {
				require.Len(t, snapshot, 1, "request %d", i+1)
				inProgress := snapshot[0]
				assert.Equal(t, final.Id, inProgress.Id, "attempts must be attributed to the original action")
				assert.Equal(t, domain.ActionStatusStarted, inProgress.ActionStatus)
				require.Len(t, inProgress.Attempts, i+1, "request %d", i+1)
				if !tt.params.RateLimited {
					assert.Nil(t, inProgress.NextRetryAt)
					continue
				}
				if assert.NotNil(t, inProgress.NextRetryAt, "request %d", i+1) {
					// Only automatic retries advance the backoff schedule.
					step := i
					if tt.params.UserRespond {
						step = 0
					}
					lastAttempt := inProgress.Attempts[i].At
					assert.False(t, inProgress.NextRetryAt.Before(lastAttempt.Add(scaleDuration(rateLimitRetryInitialBackoff<<step, 1-rateLimitRetryJitter))))
					assert.False(t, inProgress.NextRetryAt.After(lastAttempt.Add(scaleDuration(rateLimitRetryInitialBackoff<<step, 1+rateLimitRetryJitter))))
				}
			}

			// Once a retry is underway, whether the timer fired or the user
			// retried early, the consumed deadline must no longer be
			// advertised while the history stays.
			wantRetries := wantAttempts - boolToInt(tt.params.GiveUp || tt.params.CancelDuringWait)
			require.Len(t, attemptSnapshots, wantRetries)
			for i, snapshot := range attemptSnapshots {
				require.Len(t, snapshot, 1, "retry %d", i+1)
				inProgress := snapshot[0]
				assert.Equal(t, final.Id, inProgress.Id)
				assert.Equal(t, domain.ActionStatusStarted, inProgress.ActionStatus)
				assert.Len(t, inProgress.Attempts, i+1, "retry %d", i+1)
				assert.Nil(t, inProgress.NextRetryAt, "retry %d", i+1)
			}
		})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func retriedActions(actions []domain.FlowAction) []domain.FlowAction {
	var retried []domain.FlowAction
	for _, action := range actions {
		if action.ActionType == activityRetryTestActionName {
			retried = append(retried, action)
		}
	}
	return retried
}
