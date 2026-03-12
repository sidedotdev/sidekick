package dev

import (
	"context"
	"os"
	"sidekick/common"
	"sidekick/flow_action"
	"time"

	"go.temporal.io/sdk/workflow"
)

// DevRunState represents the current state of dev runs for the query response.
type DevRunState struct {
	ActiveRuns map[string]*DevRunInstance `json:"activeRuns"`
}

// UserActionType defines the type for user actions.
type UserActionType string

const (
	// UserActionDevRunStart requests starting a Dev Run.
	UserActionDevRunStart UserActionType = "dev_run_start"
	// UserActionDevRunStop requests stopping a Dev Run.
	UserActionDevRunStop UserActionType = "dev_run_stop"
)

// QueryNameDevRunConfig is the name of the query for retrieving dev run configuration.
const QueryNameDevRunConfig = "dev_run_config"

// QueryNameDevRunState is the name of the query for retrieving current dev run state.
const QueryNameDevRunState = "dev_run_state"

// SetupDevRunConfigQuery registers a query handler that returns the dev run configuration.
func SetupDevRunConfigQuery(dCtx DevContext) {
	_ = workflow.SetQueryHandler(dCtx, QueryNameDevRunConfig, func() (common.DevRunConfig, error) {
		return dCtx.RepoConfig.DevRun, nil
	})
}

// SetupDevRunStateQuery registers a query handler that returns the current dev run state.
func SetupDevRunStateQuery(dCtx DevContext) {
	_ = workflow.SetQueryHandler(dCtx, QueryNameDevRunState, func() (DevRunState, error) {
		entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
		if entry == nil {
			return DevRunState{ActiveRuns: make(map[string]*DevRunInstance)}, nil
		}
		return DevRunState{ActiveRuns: entry}, nil
	})
}

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

	flowInfo := workflow.GetInfo(dCtx)
	targetBranch := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)

	startCtx := workflow.WithActivityOptions(dCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	// Start all configured dev run commands
	for commandId := range dCtx.RepoConfig.DevRun {
		// Check if this command is already active
		var existingInstance *DevRunInstance
		existingEntry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
		if existingEntry != nil {
			if instance, ok := existingEntry[commandId]; ok {
				existingInstance = instance
			}
		}

		devRunCtx := DevRunContext{
			CommandId:    commandId,
			WorkspaceId:  dCtx.WorkspaceId,
			FlowId:       flowInfo.WorkflowExecution.ID,
			WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
			SourceBranch: dCtx.Worktree.Name,
			BaseBranch:   targetBranch,
			TargetBranch: targetBranch,
		}

		var dra *DevRunActivities
		var startOutput StartDevRunOutput
		err := workflow.ExecuteActivity(startCtx, dra.StartDevRun, StartDevRunInput{
			DevRunConfig:     dCtx.RepoConfig.DevRun,
			CommandId:        commandId,
			Context:          devRunCtx,
			ExistingInstance: existingInstance,
		}).Get(dCtx, &startOutput)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to start Dev Run", "commandId", commandId, "error", err)
			continue
		}

		// Store Dev Run instance in GlobalState (replayed on worker restart)
		if startOutput.Started {
			SetDevRunInstance(dCtx.ExecContext.GlobalState, startOutput.Instance)

			workflow.Go(dCtx, func(ctx workflow.Context) {
				monitorActiveRun(dCtx.WithContext(ctx), commandId, devRunCtx, startOutput.Instance)
			})
		}
	}
}

// monitorActiveRun watches a running Dev Run process from workflow code.
// It launches the MonitorDevRun activity (which tails output, streams events,
// and heartbeats) and cleans up workflow state when the process exits.
func monitorActiveRun(dCtx DevContext, commandId string, devRunCtx DevRunContext, instance *DevRunInstance) {
	monitorCtx := workflow.WithActivityOptions(dCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 24 * time.Hour,
		HeartbeatTimeout:    5 * time.Second,
	})

	var dra *DevRunActivities
	var monitorOutput MonitorDevRunOutput
	err := workflow.ExecuteActivity(monitorCtx, dra.MonitorDevRun, MonitorDevRunInput{
		DevRunConfig: dCtx.RepoConfig.DevRun,
		CommandId:    commandId,
		Context:      devRunCtx,
		Instance:     instance,
	}).Get(dCtx, &monitorOutput)
	if err != nil {
		workflow.GetLogger(dCtx).Debug("MonitorDevRun ended", "commandId", commandId, "error", err)
		return
	}

	// Process exited naturally; clear instance from workflow state so
	// dev run state queries reflect the process is no longer active.
	ClearDevRunInstance(dCtx.ExecContext.GlobalState, commandId)

	// Clean up the instance file used for idempotent recovery.
	flowInfo := workflow.GetInfo(dCtx)
	_ = workflow.ExecuteActivity(
		workflow.WithActivityOptions(dCtx, workflow.ActivityOptions{
			StartToCloseTimeout: 5 * time.Second,
		}),
		removeDevRunInstanceFileActivity,
		flowInfo.WorkflowExecution.ID,
		commandId,
	).Get(dCtx, nil)
}

// removeDevRunInstanceFileActivity is a small activity that removes the
// instance metadata file from disk after a dev run ends naturally.
func removeDevRunInstanceFileActivity(ctx context.Context, flowId, commandId string) error {
	os.Remove(devRunInstanceFilePath(flowId, commandId))
	return nil
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

	// Stop all active dev run instances
	for commandId, instance := range entry {
		devRunCtx := DevRunContext{
			DevRunId:     instance.DevRunId,
			CommandId:    commandId,
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
			CommandId:    commandId,
			Context:      devRunCtx,
			Instance:     instance,
		}).Get(dCtx, &stopOutput)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run", "commandId", commandId, "error", err)
		}

		// Clear this instance from GlobalState
		ClearDevRunInstance(dCtx.ExecContext.GlobalState, commandId)
	}
}
