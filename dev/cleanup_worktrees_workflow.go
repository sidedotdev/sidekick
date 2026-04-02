package dev

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const CleanupWorktreesWorkflowID = "cleanup_stale_worktrees"

func CleanupWorktreesWorkflow(ctx workflow.Context) error {
	log := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var ima *DevAgentManagerActivities

	var workspaces ListWorkspacesResult
	err := workflow.ExecuteActivity(ctx, ima.ListWorkspaces).Get(ctx, &workspaces)
	if err != nil {
		return err
	}

	for _, wsId := range workspaces.WorkspaceIds {
		var report CleanupStaleWorktreesReport
		err := workflow.ExecuteActivity(ctx, ima.CleanupStaleWorktrees, CleanupStaleWorktreesInput{
			WorkspaceId: wsId,
			DryRun:      false,
		}).Get(ctx, &report)
		if err != nil {
			log.Error("Stale worktree cleanup failed for workspace", "WorkspaceId", wsId, "Error", err)
			continue
		}
		log.Info("Stale worktree cleanup completed", "WorkspaceId", wsId, "Candidates", len(report.Candidates))
	}

	return nil
}

func StartCleanupWorktreesWorkflow(ctx context.Context, temporalClient client.Client, taskQueue string) {
	_, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    CleanupWorktreesWorkflowID,
		TaskQueue:             taskQueue,
		CronSchedule:          "0 3 * * *",
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, CleanupWorktreesWorkflow)
	if err != nil {
		log.Debug().Err(err).Msg("Cleanup worktrees workflow start (may already be running)")
	}
}
