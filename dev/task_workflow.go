package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/utils"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
)

// FlowWorkflowTaskTimeout extends the default 10s workflow task timeout for
// task and flow workflows. On a sticky cache miss, a single workflow task must
// replay the entire history within this window; long-lived flows accumulate
// tens of thousands of events, and once replay exceeds the timeout the
// workflow falls into a perpetual workflow-task retry loop that starves the
// worker. The server caps this value at 120s
// (common.MaxWorkflowTaskStartToCloseTimeout).
const FlowWorkflowTaskTimeout = time.Minute

type TaskWorkflowInput struct {
	WorkspaceId string
	TaskId      string
	FlowType    string
	FlowOptions map[string]interface{}
	Description string
	// Title is required for IDD flows, which carry no description and use the
	// title for branch naming and the intent commit message.
	Title string

	// ExistingFlowId, when set, puts the workflow into "monitor mode":
	// it skips child creation and listens for signals from an already-running
	// flow. Used when re-executing the parent after a child flow reset.
	ExistingFlowId string
}

func TaskWorkflow(ctx workflow.Context, input TaskWorkflowInput) error {
	log := workflow.GetLogger(ctx)
	ctx = setActivityOptions(ctx)
	var ima *DevAgentManagerActivities

	var childFuture workflow.ChildWorkflowFuture
	var flow domain.Flow

	if input.ExistingFlowId != "" {
		// Monitor mode: adopt an already-running flow instead of starting a
		// new child. Used after a flow reset to re-establish signal handling.
		err := workflow.ExecuteActivity(ctx, ima.GetWorkflow, input.WorkspaceId, input.ExistingFlowId).Get(ctx, &flow)
		if err != nil {
			return fmt.Errorf("failed to get existing flow: %w", err)
		}
	} else {
		if input.FlowType != "basic_dev" && input.FlowType != "planned_dev" && input.FlowType != "idd" {
			return fmt.Errorf("invalid flow type '%s'; valid values are 'basic_dev', 'planned_dev' and 'idd'", input.FlowType)
		}

		var workspace domain.Workspace
		err := workflow.ExecuteActivity(ctx, ima.FindWorkspaceById, input.WorkspaceId).Get(ctx, &workspace)
		if err != nil {
			return fmt.Errorf("failed to find workspace: %w", err)
		}

		flowId := "flow_" + ksuidSideEffect(ctx)
		childOptions := workflow.ChildWorkflowOptions{
			WorkflowID:        flowId,
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
		}
		if workflow.GetVersion(ctx, "child-flow-sidekick-version-memo", workflow.DefaultVersion, 1) == 1 {
			childOptions.Memo = map[string]interface{}{
				"sidekickVersion": sidekickVersionSideEffect(ctx),
			}
		}
		if workflow.GetVersion(ctx, "flow-workflow-task-timeout", workflow.DefaultVersion, 1) == 1 {
			childOptions.WorkflowTaskTimeout = FlowWorkflowTaskTimeout
		}
		childCtx := workflow.WithChildOptions(ctx, childOptions)

		untypedOptions := input.FlowOptions
		var configOverrides common.ConfigOverrides
		switch input.FlowType {
		case "basic_dev":
			var options BasicDevOptions
			utils.Transcode(untypedOptions, &options)
			configOverrides = options.ConfigOverrides
			childFuture = workflow.ExecuteChildWorkflow(childCtx, BasicDevWorkflow, BasicDevWorkflowInput{
				WorkspaceId:     input.WorkspaceId,
				Requirements:    input.Description,
				RepoDir:         workspace.LocalRepoDir,
				BasicDevOptions: options,
			})
		case "planned_dev":
			var options PlannedDevOptions
			utils.Transcode(untypedOptions, &options)
			configOverrides = options.ConfigOverrides
			childFuture = workflow.ExecuteChildWorkflow(childCtx, PlannedDevWorkflow, PlannedDevInput{
				WorkspaceId:       input.WorkspaceId,
				Requirements:      input.Description,
				RepoDir:           workspace.LocalRepoDir,
				PlannedDevOptions: options,
			})
		case "idd":
			if strings.TrimSpace(input.Title) == "" {
				return fmt.Errorf("a non-empty title is required for the 'idd' flow type")
			}
			var options IddOptions
			utils.Transcode(untypedOptions, &options)
			configOverrides = options.ConfigOverrides
			childFuture = workflow.ExecuteChildWorkflow(childCtx, IddWorkflow, IddWorkflowInput{
				WorkspaceId: input.WorkspaceId,
				RepoDir:     workspace.LocalRepoDir,
				TaskId:      input.TaskId,
				Title:       input.Title,
				IddOptions:  options,
			})
		}

		// Wait for child workflow to actually start
		var we workflow.Execution
		err = childFuture.GetChildWorkflowExecution().Get(childCtx, &we)
		if err != nil {
			return fmt.Errorf("child workflow failed to start: %w", err)
		}

		// Persist the flow record
		flow = domain.Flow{
			WorkspaceId: input.WorkspaceId,
			Id:          we.ID,
			Type:        domain.FlowType(input.FlowType),
			Status:      "in_progress",
			ParentId:    input.TaskId,
		}
		err = workflow.ExecuteActivity(ctx, ima.PutWorkflow, flow).Get(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to persist flow record: %w", err)
		}

		titleVersion := workflow.GetVersion(ctx, "generate-task-title", workflow.DefaultVersion, 2)
		if titleVersion >= 1 && input.FlowType != "idd" {
			workflow.Go(ctx, func(gCtx workflow.Context) {
				gCtx = setActivityOptions(gCtx)
				titleInput := GenerateTitleInput{
					WorkspaceId: input.WorkspaceId,
					TaskId:      input.TaskId,
					Description: input.Description,
				}
				var titleErr error
				if titleVersion >= 2 {
					titleErr = generateTaskTitle(gCtx, titleInput, workspace.LocalRepoDir, configOverrides)
				} else {
					titleErr = workflow.ExecuteActivity(gCtx, ima.GenerateTaskTitle, titleInput).Get(gCtx, nil)
				}
				if titleErr != nil {
					workflow.GetLogger(gCtx).Warn("Failed to generate task title", "Error", titleErr)
				}
			})
		}
	}

	// Signal-handling loop: listen for signals from the child and monitor its completion
	requestForUserCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
	workflowClosedCh := workflow.GetSignalChannel(ctx, SignalNameWorkflowClosed)

	childDone := false
	for !childDone {
		selector := workflow.NewNamedSelector(ctx, "taskWorkflowSelector")

		selector.AddReceive(requestForUserCh, func(c workflow.ReceiveChannel, _ bool) {
			var req flow_action.RequestForUser
			c.Receive(ctx, &req)
			log.Info("Request for user signal received", "FlowActionId", req.FlowActionId)

			createErr := workflow.ExecuteActivity(ctx, ima.CreatePendingUserRequest, input.WorkspaceId, req).Get(ctx, nil)
			if createErr != nil {
				log.Error("Failed to create pending user request", "Error", createErr)
				return
			}

			status := domain.TaskStatusBlocked
			if req.RequestKind == flow_action.RequestKindMergeApproval {
				status = domain.TaskStatusInReview
			}
			update := TaskUpdate{
				Status:    status,
				AgentType: domain.AgentTypeHuman,
			}
			updateErr := workflow.ExecuteActivity(ctx, ima.UpdateTaskByTaskId, input.WorkspaceId, input.TaskId, update).Get(ctx, nil)
			if updateErr != nil {
				log.Error("Failed to update task", "Error", updateErr)
			}
		})

		selector.AddReceive(workflowClosedCh, func(c workflow.ReceiveChannel, _ bool) {
			var closure WorkflowClosure
			c.Receive(ctx, &closure)
			log.Info("Received workflow closure", "FlowId", closure.FlowId, "Reason", closure.Reason)

			flow.Status = closure.Reason
			putErr := workflow.ExecuteActivity(ctx, ima.PutWorkflow, flow).Get(ctx, nil)
			if putErr != nil {
				log.Error("Failed to update flow status", "Error", putErr)
				return
			}

			completeErr := workflow.ExecuteActivity(ctx, ima.CompleteFlowParentTask, input.WorkspaceId, input.TaskId, flow.Status).Get(ctx, nil)
			if completeErr != nil {
				log.Error("Failed to complete parent task", "Error", completeErr)
			}
			childDone = true
		})

		if childFuture != nil {
			selector.AddFuture(childFuture, func(f workflow.Future) {
				var childErr error
				childErr = f.Get(ctx, nil)
				status := "completed"
				if childErr != nil {
					log.Error("Child workflow failed", "Error", childErr)
					status = "failed"
				}

				flow.Status = status
				putErr := workflow.ExecuteActivity(ctx, ima.PutWorkflow, flow).Get(ctx, nil)
				if putErr != nil {
					log.Error("Failed to update flow status on child completion", "Error", putErr)
				}

				completeErr := workflow.ExecuteActivity(ctx, ima.CompleteFlowParentTask, input.WorkspaceId, input.TaskId, flow.Status).Get(ctx, nil)
				if completeErr != nil {
					log.Error("Failed to complete parent task on child completion", "Error", completeErr)
				}
				childDone = true
			})
		}

		selector.Select(ctx)
	}

	return nil
}
