package dev

import (
	"context"
	"sidekick/common"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestManageChatHistoryRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		failures     int
		nonRetryable bool
		wantAttempts int
		wantPanic    bool
	}{
		{name: "success on tenth attempt", failures: 9, wantAttempts: 10},
		{name: "exhausted timeouts", failures: 10, wantAttempts: 10, wantPanic: true},
		{name: "non-retryable failure", failures: 1, nonRetryable: true, wantAttempts: 1, wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			history := &persisted_ai.ChatHistoryContainer{
				History: persisted_ai.NewLlm2ChatHistory("test-flow", "test-workspace-id"),
			}
			managedHistory := persisted_ai.NewLlm2ChatHistory("test-flow", "test-workspace-id")
			managedRefs := []persisted_ai.MessageRef{
				{Role: "user", BlockKeys: []string{"managed-block"}},
			}
			managedHistory.SetRefs(managedRefs)

			var attemptTimes []time.Time
			var attempts []int32
			var timeouts []time.Duration
			var ca *persisted_ai.ChatHistoryActivities
			env.OnActivity(ca.ManageV4, mock.Anything, mock.Anything).Return(
				func(ctx context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
					info := activity.GetInfo(ctx)
					attemptTimes = append(attemptTimes, env.Now())
					attempts = append(attempts, info.Attempt)
					timeouts = append(timeouts, info.StartToCloseTimeout)
					if len(attempts) <= tt.failures {
						if tt.nonRetryable {
							return nil, temporal.NewNonRetryableApplicationError("invalid history", "InvalidHistory", nil)
						}
						return nil, temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil)
					}
					return &persisted_ai.ManageOutput{
						ChatHistory: &persisted_ai.ChatHistoryContainer{History: managedHistory},
					}, nil
				},
			)

			env.ExecuteWorkflow(func(ctx workflow.Context) (*persisted_ai.ChatHistoryContainer, error) {
				ctx = utils.NoRetryCtx(ctx)
				ManageChatHistory(ctx, history, "test-workspace-id", 100, common.ModelConfig{})
				return history, nil
			})

			require.True(t, env.IsWorkflowCompleted())
			require.Len(t, attempts, tt.wantAttempts)
			for i, attempt := range attempts {
				require.Equal(t, int32(i+1), attempt)
				require.Equal(t, 5*time.Minute, timeouts[i])
				if i > 0 {
					delay := time.Second * time.Duration(1<<uint(i-1))
					require.WithinDuration(t, attemptTimes[i-1].Add(delay), attemptTimes[i], time.Second)
				}
			}
			if tt.wantPanic {
				var panicErr *temporal.PanicError
				require.ErrorAs(t, env.GetWorkflowError(), &panicErr)
				require.Contains(t, panicErr.Error(), "ManageChatHistory ManageV4 activity returned an error")
			} else {
				require.NoError(t, env.GetWorkflowError())
				var result *persisted_ai.ChatHistoryContainer
				require.NoError(t, env.GetWorkflowResult(&result))
				require.Equal(t, managedRefs, result.History.(*persisted_ai.Llm2ChatHistory).Refs())
			}
			env.AssertExpectations(t)
		})
	}
}
