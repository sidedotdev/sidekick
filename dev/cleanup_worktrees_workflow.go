package dev

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
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

func StartCleanupWorktreesSchedule(ctx context.Context, temporalClient client.Client, taskQueue string) {
	scheduleID := CleanupWorktreesWorkflowID + "_schedule"
	_, err := temporalClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: scheduleID,
		Spec: client.ScheduleSpec{
			CronExpressions: []string{"0 3 * * *"},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        CleanupWorktreesWorkflowID,
			Workflow:  CleanupWorktreesWorkflow,
			TaskQueue: taskQueue,
		},
	})
	if err != nil {
		if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
			log.Debug().Msg("Cleanup worktrees schedule already exists")
		} else {
			log.Warn().Err(err).Msg("Failed to create cleanup worktrees schedule")
		}
	}
}
