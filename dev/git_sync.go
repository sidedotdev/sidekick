package dev

import (
	"fmt"
	"time"

	"sidekick/coding/git"

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
	err := workflow.ExecuteActivity(ctx, git.SyncGitActivity, git.SyncGitInput{
		EnvContainer: *dCtx.EnvContainer,
	}).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("checkpoint git: %w", err)
	}
	return nil
}
