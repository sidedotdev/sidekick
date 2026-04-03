package dev

import (
	"fmt"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/utils"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
)

type TaskWorkflowInput struct {
	WorkspaceId string
	TaskId      string
	FlowType    string
	FlowOptions map[string]interface{}
	Description string
}

func TaskWorkflow(ctx workflow.Context, input TaskWorkflowInput) error {
	log := workflow.GetLogger(ctx)

	if input.FlowType != "basic_dev" && input.FlowType != "planned_dev" {
		return fmt.Errorf("invalid flow type '%s'; valid values are 'basic_dev' and 'planned_dev'", input.FlowType)
	}

	ctx = setActivityOptions(ctx)
	var ima *DevAgentManagerActivities

	var workspace domain.Workspace
	err := workflow.ExecuteActivity(ctx, ima.FindWorkspaceById, input.WorkspaceId).Get(ctx, &workspace)
	if err != nil {
		return fmt.Errorf("failed to find workspace: %w", err)
	}

	flowId := "flow_" + ksuidSideEffect(ctx)
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        flowId,
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})

	var childFuture workflow.ChildWorkflowFuture
	untypedOptions := input.FlowOptions
	switch input.FlowType {
	case "basic_dev":
		var options BasicDevOptions
		utils.Transcode(untypedOptions, &options)
		childFuture = workflow.ExecuteChildWorkflow(childCtx, BasicDevWorkflow, BasicDevWorkflowInput{
			WorkspaceId:     input.WorkspaceId,
			Requirements:    input.Description,
			RepoDir:         workspace.LocalRepoDir,
			BasicDevOptions: options,
		})
	case "planned_dev":
		var options PlannedDevOptions
		utils.Transcode(untypedOptions, &options)
		childFuture = workflow.ExecuteChildWorkflow(childCtx, PlannedDevWorkflow, PlannedDevInput{
			WorkspaceId:       input.WorkspaceId,
			Requirements:      input.Description,
			RepoDir:           workspace.LocalRepoDir,
			PlannedDevOptions: options,
		})
	}

	// Wait for child workflow to actually start
	var we workflow.Execution
	err = childFuture.GetChildWorkflowExecution().Get(childCtx, &we)
	if err != nil {
		return fmt.Errorf("child workflow failed to start: %w", err)
	}

	// Persist the flow record
	flow := domain.Flow{
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

		selector.Select(ctx)
	}

	return nil
}
