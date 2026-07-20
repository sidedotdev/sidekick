package dev

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
)

// TestHibernateSignalDoesNotBlockSelectorCallback verifies that handling a
// hibernate signal does not panic with "trying to block on coroutine which is
// already blocked", which happens when the handler blocks on an activity
// inside a Selector receive callback.
func TestHibernateSignalDoesNotBlockSelectorCallback(t *testing.T) {
	t.Parallel()
	testSuite := &testsuite.WorkflowTestSuite{}
	wfEnv := testSuite.NewTestWorkflowEnvironment()

	gs := &flow_action.GlobalState{}
	gs.InitValues()

	testWorkflow := func(ctx workflow.Context) error {
		activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
		})
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     activityCtx,
				WorkspaceId: "test-workspace",
				GlobalState: gs,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{
						WorkingDirectory: "/tmp/test-repo",
					},
				},
			},
			Worktree: &domain.Worktree{
				Name: "side/test-branch",
			},
		}

		SetupHibernateHandler(dCtx)

		// Wait long enough for the signal to be received and the
		// hibernation activity to complete.
		_ = workflow.Sleep(ctx, 100*time.Millisecond)

		return nil
	}

	wfEnv.RegisterWorkflow(testWorkflow)

	wfEnv.OnActivity(git.HibernateWorktreeActivity, mock.Anything, mock.Anything).
		Return(git.HibernateWorktreeOutput{}, nil).Once()

	wfEnv.RegisterDelayedCallback(func() {
		wfEnv.SignalWorkflow(SignalNameHibernate, HibernateSignal{})
	}, 10*time.Millisecond)

	wfEnv.ExecuteWorkflow(testWorkflow)

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	wfEnv.AssertExpectations(t)
	assert.Equal(t, true, gs.GetValue(globalStateKeyHibernated))
	assert.True(t, gs.Paused)
}
