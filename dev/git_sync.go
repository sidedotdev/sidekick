package dev

import (
	"fmt"
	"time"

	"sidekick/env"

	"go.temporal.io/sdk/workflow"
)

func checkpointSubflowGit(dCtx DevContext, subflowType string) error {
	switch subflowType {
	case "edit_code", "commit", "step.dev", "perform_step",
		"resolve_merge_conflicts", "review_and_resolve", "pass_tests":
	default:
		return nil
	}
	if dCtx.EnvContainer == nil || !env.SupportsGitSync(dCtx.EnvContainer.Env) {
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
	err := workflow.ExecuteActivity(ctx, env.SyncGitActivity, env.SyncGitInput{
		EnvContainer: *dCtx.EnvContainer,
	}).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("checkpoint git after %s: %w", subflowType, err)
	}
	return nil
}
