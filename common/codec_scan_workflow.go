package common

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func pagedCodecCleanupWorkflow(ctx workflow.Context, input CodecCleanupWorkflowInput) error {
	input.Pending = nil
	input.Retention = input.retention()
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Second,
			MaximumInterval: time.Minute,
			MaximumAttempts: 3,
		},
	})
	if err := workflow.SetQueryHandler(ctx, "scan", func() (*CodecScanWorkflowState, error) {
		return input.Scan, nil
	}); err != nil {
		return err
	}

	// Older workers may still send completion notifications. They must not
	// authorize deletion: reset runs can share the completed run's history.
	for _, name := range []string{codecCleanupSignalName, codecCleanupResetSignalName} {
		ch := workflow.GetSignalChannel(ctx, name)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				ch.Receive(ctx, nil)
			}
		})
	}

	for pages := 0; ; pages++ {
		if pages >= 200 || workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return workflow.NewContinueAsNewError(ctx, CodecPayloadCleanupWorkflow, input)
		}
		if input.Scan == nil {
			if !input.RunOnce && !input.LastOrphanScanAt.IsZero() {
				wait := input.LastOrphanScanAt.Add(codecOrphanScanInterval).Sub(workflow.Now(ctx))
				if wait > 0 {
					if err := workflow.Sleep(ctx, wait); err != nil {
						return err
					}
				}
			}
			input.Scan = &CodecScanWorkflowState{
				ScanID:    workflow.GetInfo(ctx).WorkflowExecution.RunID,
				StartedAt: workflow.Now(ctx),
				Phase:     "begin",
			}
		}
		state := input.Scan
		err := executeCodecScanPage(actCtx, state, input)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if state.Phase == "drop" {
				return err
			}
			workflow.GetLogger(ctx).Error("Codec scan aborted", "scanId", state.ScanID, "error", err)
			state.Error = err.Error()
			if len(state.Error) > 2048 {
				state.Error = state.Error[:2048]
			}
			state.Phase = "drop"
			state.Token = nil
			state.Cursor = ""
			continue
		}
		if state.Phase != "done" {
			continue
		}
		workflow.GetLogger(ctx).Info("Codec scan finished",
			"scanId", state.ScanID, "dryRun", input.DryRun,
			"executions", state.Executions, "candidates", state.Candidates,
			"bytes", state.Bytes, "skippedOversize", state.SkippedOversize,
			"error", state.Error)
		if input.RunOnce {
			if state.Error != "" {
				return fmt.Errorf("codec scan failed: %s", state.Error)
			}
			return nil
		}
		input.LastOrphanScanAt = workflow.Now(ctx)
		input.Scan = nil
		return workflow.NewContinueAsNewError(ctx, CodecPayloadCleanupWorkflow, input)
	}
}

func executeCodecScanPage(ctx workflow.Context, state *CodecScanWorkflowState, input CodecCleanupWorkflowInput) error {
	var a *CodecCleanupActivities
	page := CodecScanPageInput{
		ScanID: state.ScanID, Token: state.Token,
		Cursor: state.Cursor, Executions: state.Executions,
	}
	var result CodecScanPageResult
	var activityFn any
	switch state.Phase {
	case "begin":
		err := workflow.ExecuteActivity(ctx, a.BeginCodecScan, CodecScanInput{
			ScanID: state.ScanID, StartedAt: state.StartedAt,
			Retention: input.Retention, DryRun: input.DryRun,
		}).Get(ctx, &result)
		if err == nil {
			state.Phase = "mark"
		}
		return err
	case "mark":
		activityFn = a.MarkCodecScanPage
	case "seal":
		activityFn = a.SealCodecScan
	case "sweep":
		activityFn = a.SweepCodecScanPage
	case "drop":
		activityFn = a.DropCodecScanPage
	default:
		return fmt.Errorf("invalid codec scan phase %q", state.Phase)
	}
	if err := workflow.ExecuteActivity(ctx, activityFn, page).Get(ctx, &result); err != nil {
		return err
	}
	switch state.Phase {
	case "mark":
		state.Executions += result.Executions
		state.Token = result.NextToken
		if result.Done {
			state.Token = nil
			state.Phase = "seal"
		}
	case "seal":
		state.Phase = "sweep"
	case "sweep":
		state.Candidates += result.Candidates
		state.Bytes += result.Bytes
		state.SkippedOversize += result.SkippedOversize
		state.Cursor = result.NextCursor
		if result.Done {
			state.Cursor = ""
			state.Phase = "drop"
		}
	case "drop":
		state.Cursor = result.NextCursor
		if result.Done {
			state.Cursor = ""
			state.Phase = "done"
		}
	}
	return nil
}
