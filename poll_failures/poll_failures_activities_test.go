package poll_failures

import (
	"context"
	"sidekick/mocks"
	"sidekick/srv/redis"
	"sidekick/utils"
	"testing"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/common/v1"
	workflowApi "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// setupTestEnvironment sets up the necessary mocked and real components for testing.
func setupTestEnvironment(t *testing.T) (*PollFailuresActivities, *mocks.Client) {
	// Create a real Redis database instance
	redisDB := redis.NewService()

	// Create a mocked Temporal client
	mockTemporalClient := mocks.NewClient(t)

	return &PollFailuresActivities{
		TemporalClient:   mockTemporalClient,
		DatabaseAccessor: redisDB,
	}, mockTemporalClient
}

func TestListFailedWorkflows(t *testing.T) {
	// Setup the activity with the mocked client
	activities, temporalClient := setupTestEnvironment(t)

	// Define expected results
	expectedWorkflows := []string{"workflow1", "workflow2"}
	workflowExecutionInfos := utils.Map(expectedWorkflows, func(w string) *workflowApi.WorkflowExecutionInfo {
		return &workflowApi.WorkflowExecutionInfo{
			Execution: &common.WorkflowExecution{
				WorkflowId: w,
			},
		}
	})
	// Mock the Temporal client to return expected workflows
	temporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(
		&workflowservice.ListWorkflowExecutionsResponse{
			Executions: workflowExecutionInfos,
		}, nil).Once()

	// Execute the activity
	input := ListFailedWorkflowsInput{
		WorkspaceId: "workspace1",
	}
	result, err := activities.ListFailedWorkflows(context.Background(), input)
	workflows := utils.Map(result, func(w *workflowApi.WorkflowExecutionInfo) string {
		return w.Execution.WorkflowId
	})

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the result matches expected workflows
	assert.Equal(t, expectedWorkflows, workflows)
}

func TestUpdateTaskStatus(t *testing.T) {
	// Set up the test environment
	activities, _ := setupTestEnvironment(t)

	// Create a workflow record
	workflow := &domain.Flow{
		Id:          "workflow1",
		WorkspaceId: "workspace1",
		ParentId:    "task_1",
	}
	err := activities.DatabaseAccessor.PersistWorkflow(context.Background(), *workflow)
	assert.NoError(t, err)

	task := &domain.Task{
		Id:          "task_1",
		WorkspaceId: "workspace1",
		Status:      domain.TaskStatusToDo,
	}
	err = activities.DatabaseAccessor.PersistTask(context.Background(), *task)
	assert.NoError(t, err)

	// Call the UpdateTaskStatus method
	input := UpdateTaskStatusInput{
		WorkspaceId: "workspace1",
		FlowId:      "workflow1",
	}
	err = activities.UpdateTaskStatus(context.Background(), input)
	assert.NoError(t, err)

	// Retrieve the updated task
	updatedTask, err := activities.DatabaseAccessor.GetTask(context.Background(), "workspace1", "task_1")
	assert.NoError(t, err)

	// Assert the task status is updated to TaskStatusFailed
	assert.Equal(t, domain.TaskStatusFailed, updatedTask.Status)
	assert.Equal(t, domain.TaskStatusFailed, updatedTask.Status)
}
