package dev

import (
	"fmt"
	"time"

	"sidekick/coding/git"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

func checkpointSubflowGit(dCtx DevContext) error {
	if dCtx.EnvContainer == nil || !git.SupportsGitSync(dCtx.EnvContainer.Env) {
		return nil
	}
	if workflow.GetVersion(dCtx, "subflow-git-checkpoint", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return nil
	}

	// An interrupted agent can already have committed useful work.
	ctx, cancel := workflow.NewDisconnectedContext(dCtx)
	defer cancel()
	options := workflow.GetActivityOptions(ctx)
	options.StartToCloseTimeout = 3 * time.Minute
	options.HeartbeatTimeout = 0
	ctx = workflow.WithActivityOptions(ctx, options)
	input := git.SyncGitInput{EnvContainer: *dCtx.EnvContainer}
	var err error
	if workflow.GetVersion(ctx, "git-checkpoint-user-retry", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		err = workflow.ExecuteActivity(ctx, git.SyncGitActivity, input).Get(ctx, nil)
	} else {
		actionCtx := flow_action.ActionContext{
			ExecContext: dCtx.WithContext(ctx).ExecContext,
			ActionType:  "Checkpoint git",
		}
		err = flow_action.PerformWithUserRetryOptions(actionCtx, flow_action.UserRetryOptions{
			PropagateCancellation: true,
			OnFailure: flow_action.ActivityFailureHandlerFunc(func(eCtx flow_action.ExecContext, actionName string, activityErr error) (flow_action.RetryDecision, error) {
				// Cleanup survives cancellation, but waiting for user input must not.
				if dCtx.Err() != nil {
					return flow_action.RetryDecisionFail, nil
				}
				eCtx.Context = dCtx.Context
				return (flow_action.ContinuePromptFailureHandler{}).HandleActivityFailure(eCtx, actionName, activityErr)
			}),
		}, git.SyncGitActivity, nil, input)
	}
	if err != nil {
		return fmt.Errorf("checkpoint git: %w", err)
	}
	return nil
}
