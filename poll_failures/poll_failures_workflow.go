package poll_failures

import (
	"log"

	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// PollFailuresWorkflow checks for failed workflows and updates task statuses accordingly.
type PollFailuresWorkflowInput struct {
	WorkspaceId string
}

func PollFailuresWorkflow(ctx workflow.Context, input PollFailuresWorkflowInput) error {
	ctx = utils.DefaultRetryCtx(ctx)

	activities := &PollFailuresActivities{} // nil pointer struct is how we use struct activities

	var failedWorkflows []workflow.Execution
	err := workflow.ExecuteActivity(ctx, activities.ListFailedWorkflows, ListFailedWorkflowsInput(input)).Get(ctx, &failedWorkflows)
	if err != nil {
		return err
	}

	for _, workflowExecution := range failedWorkflows {
		err = workflow.ExecuteActivity(ctx, activities.UpdateTaskStatus, UpdateTaskStatusInput{
			WorkspaceId: input.WorkspaceId,
			FlowId:      workflowExecution.ID,
		}).Get(ctx, nil)
		if err != nil {
			log.Println("Error updating task status:", err)
			continue
		}
	}

	return nil
}
