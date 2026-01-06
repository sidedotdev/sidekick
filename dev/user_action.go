package dev

import (
	"sidekick/common"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

// UserActionType defines the type for user actions.
type UserActionType string

const (
	// UserActionDevRunStart requests starting a Dev Run.
	UserActionDevRunStart UserActionType = "dev_run_start"
	// UserActionDevRunStop requests stopping a Dev Run.
	UserActionDevRunStop UserActionType = "dev_run_stop"
)

// SetupUserActionHandler sets up a signal handler for user actions like "go_next_step"
// and Dev Run start/stop. It listens on the "user_action" signal channel.
func SetupUserActionHandler(dCtx DevContext) {
	signalChan := workflow.GetSignalChannel(dCtx, SignalNameUserAction)

	workflow.Go(dCtx, func(ctx workflow.Context) {
		for {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
				var action string
				c.Receive(ctx, &action)

				switch action {
				case string(flow_action.UserActionGoNext):
					dCtx.ExecContext.GlobalState.SetUserAction(flow_action.UserActionGoNext)

				case string(UserActionDevRunStart):
					// Spawn a new coroutine to avoid blocking inside the selector callback
					workflow.Go(ctx, func(goCtx workflow.Context) {
						handleDevRunStart(dCtx.WithContext(goCtx))
					})

				case string(UserActionDevRunStop):
					// Spawn a new coroutine to avoid blocking inside the selector callback
					workflow.Go(ctx, func(goCtx workflow.Context) {
						handleDevRunStop(dCtx.WithContext(goCtx))
					})
				}
			})
			selector.Select(ctx)
			if ctx.Err() != nil {
				return
			}
		}
	})
}

func handleDevRunStart(dCtx DevContext) {
	if dCtx.Worktree == nil {
		return
	}

	// Check if a Dev Run is already active
	existingEntry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
	if existingEntry != nil {
		// Check if the processes are still alive
		if AreProcessesAlive(existingEntry.Pgids) {
			workflow.GetLogger(dCtx).Warn("Dev Run already active, ignoring start request")
			return
		}
		// Processes exited naturally, clear stale entry
		ClearDevRunEntry(dCtx.ExecContext.GlobalState)
	}

	flowInfo := workflow.GetInfo(dCtx)
	targetBranch := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)
	devRunCtx := DevRunContext{
		WorkspaceId:  dCtx.WorkspaceId,
		FlowId:       flowInfo.WorkflowExecution.ID,
		WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
		SourceBranch: dCtx.Worktree.Name,
		BaseBranch:   targetBranch,
		TargetBranch: targetBranch,
	}

	var dra *DevRunActivities
	var startOutput StartDevRunOutput
	err := workflow.ExecuteActivity(dCtx, dra.StartDevRun, StartDevRunInput{
		DevRunConfig: dCtx.RepoConfig.DevRun,
		Context:      devRunCtx,
	}).Get(dCtx, &startOutput)
	if err != nil {
		workflow.GetLogger(dCtx).Warn("Failed to start Dev Run", "error", err)
		return
	}

	// Store Dev Run entry in GlobalState (replayed on worker restart)
	if startOutput.Started {
		SetDevRunEntry(dCtx.ExecContext.GlobalState, startOutput.DevRunId, startOutput.Entry)
	}
}

func handleDevRunStop(dCtx DevContext) {
	if dCtx.Worktree == nil {
		return
	}

	flowInfo := workflow.GetInfo(dCtx)
	targetBranch := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)

	// Retrieve Dev Run entry from GlobalState
	entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
	if entry == nil {
		// No active Dev Run - this is normal if user clicks stop after natural exit
		// or if stop is called multiple times
		workflow.GetLogger(dCtx).Debug("No active Dev Run to stop")
		return
	}

	devRunCtx := DevRunContext{
		DevRunId:     entry.DevRunId,
		WorkspaceId:  dCtx.WorkspaceId,
		FlowId:       flowInfo.WorkflowExecution.ID,
		WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
		SourceBranch: dCtx.Worktree.Name,
		BaseBranch:   targetBranch,
		TargetBranch: targetBranch,
	}

	var dra *DevRunActivities
	var stopOutput StopDevRunOutput
	err := workflow.ExecuteActivity(dCtx, dra.StopDevRun, StopDevRunInput{
		DevRunConfig: dCtx.RepoConfig.DevRun,
		Context:      devRunCtx,
		Entry:        entry,
	}).Get(dCtx, &stopOutput)
	if err != nil {
		workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run", "error", err)
	}

	// Clear stored Dev Run state
	ClearDevRunEntry(dCtx.ExecContext.GlobalState)
}
