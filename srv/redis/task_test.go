package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestPersistTask(t *testing.T) {
	db := NewTestRedisStorage()

	taskRecord := domain.Task{
		WorkspaceId: "test-workspace",
		Id:          "test-id",
		Title:       "test-title",
		Description: "test-description",
		Status:      domain.TaskStatusToDo,
	}

	err := db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the TaskRecord was correctly persisted to the Redis database
	persistedTaskJson, err := db.Client.Get(context.Background(), fmt.Sprintf("%s:%s", taskRecord.WorkspaceId, taskRecord.Id)).Result()
	assert.Nil(t, err)
	var persistedTask domain.Task
	err = json.Unmarshal([]byte(persistedTaskJson), &persistedTask)
	assert.Nil(t, err)
	assert.Equal(t, taskRecord, persistedTask)

	// Verify that the status-specific kanban set has it
	statusKey := fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	isMember, err := db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)

	// Change status and persist
	taskRecord.Status = domain.TaskStatusInProgress
	err = db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the task status was correctly updated in the set
	statusKey = fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	isMember, err = db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)

	// Verify that the task is not in other status sets
	for _, status := range []domain.TaskStatus{domain.TaskStatusToDo, domain.TaskStatusComplete, domain.TaskStatusBlocked, domain.TaskStatusFailed, domain.TaskStatusCanceled} {
		statusKey := fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, status)
		isMember, err := db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
		assert.Nil(t, err)
		assert.False(t, isMember)
	}

	// Archive the task
	now := time.Now()
	taskRecord.Archived = &now
	err = db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the task is in the archived set
	archivedKey := fmt.Sprintf("%s:archived_tasks", taskRecord.WorkspaceId)
	isMember, err = db.Client.SIsMember(context.Background(), archivedKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)

	// Verify that the task is not in any kanban set
	for _, status := range []domain.TaskStatus{domain.TaskStatusDrafting, domain.TaskStatusToDo, domain.TaskStatusInProgress, domain.TaskStatusComplete, domain.TaskStatusBlocked, domain.TaskStatusFailed, domain.TaskStatusCanceled} {
		statusKey := fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, status)
		isMember, err := db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
		assert.Nil(t, err)
		assert.False(t, isMember)
	}

	// Unarchive the task
	taskRecord.Archived = nil
	taskRecord.Status = domain.TaskStatusComplete
	err = db.PersistTask(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Verify that the task is not in the archived set
	isMember, err = db.Client.SIsMember(context.Background(), archivedKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.False(t, isMember)

	// Verify that the task is in the correct kanban set
	statusKey = fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	isMember, err = db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)
}

func TestGetTasks(t *testing.T) {
	db := NewTestRedisStorage()
	ctx := context.Background()

	taskRecords := []domain.Task{
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusToDo,
		},
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusInProgress,
		},
		{
			WorkspaceId: "TEST_WORKSPACE_ID",
			Id:          "task_" + ksuid.New().String(),
			Status:      domain.TaskStatusBlocked,
		},
	}

	for _, taskRecord := range taskRecords {
		err := db.PersistTask(ctx, taskRecord)
		assert.Nil(t, err)
	}

	// Call GetTasks with different combinations of statuses
	statuses := []domain.TaskStatus{taskRecords[0].Status, taskRecords[1].Status}
	retrievedTasks, err := db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, taskRecords[:2], retrievedTasks)

	statuses = []domain.TaskStatus{taskRecords[0].Status, taskRecords[2].Status}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []domain.Task{taskRecords[0], taskRecords[2]}, retrievedTasks)

	statuses = []domain.TaskStatus{taskRecords[2].Status}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []domain.Task{taskRecords[2]}, retrievedTasks)

	// we should return no tasks in the following case
	statuses = []domain.TaskStatus{"statusWithoutAnyTasks"}
	retrievedTasks, err = db.GetTasks(ctx, "TEST_WORKSPACE_ID", statuses)
	assert.Nil(t, err)
	assert.ElementsMatch(t, []domain.Task{}, retrievedTasks)
}

func TestDeleteTask(t *testing.T) {
	db := NewTestRedisStorage()
	ctx := context.Background()

	// Create a new task
	task := domain.Task{
		WorkspaceId: "test-workspace",
		Id:          "test-task-id",
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      domain.TaskStatusToDo,
	}
	err := db.PersistTask(ctx, task)
	if err != nil {
		t.Fatalf("Failed to persist task: %v", err)
	}

	// check if task has been added to the main record
	_, err = db.GetTask(ctx, task.WorkspaceId, task.Id)
	if err != nil {
		t.Fatalf("Task was not persisted (main record)")
	}

	// Check if task has been added to the kanban sets
	tasks, err := db.GetTasks(ctx, task.WorkspaceId, []domain.TaskStatus{domain.TaskStatusToDo})
	if err != nil {
		t.Fatalf("Failed to get tasks from kanban set: %v", err)
	}
	assert.Contains(t, tasks, task)

	// Delete the task
	err = db.DeleteTask(ctx, task.WorkspaceId, task.Id)
	if err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}

	// Check if task has been deleted from the main record
	_, err = db.GetTask(ctx, task.WorkspaceId, task.Id)
	if err == nil {
		t.Fatalf("Task was not deleted from the main record")
	}

	// Check if task has been deleted from the kanban sets
	tasks, err = db.GetTasks(ctx, task.WorkspaceId, []domain.TaskStatus{domain.TaskStatusToDo})
	if err != nil {
		t.Fatalf("Failed to get tasks from kanban set: %v", err)
	}
	assert.NotContains(t, tasks, task)
}
func TestAddTaskChange(t *testing.T) {
	db := NewTestRedisStreamer()
	workspaceId := "TEST_WORKSPACE_ID"
	taskRecord := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      domain.TaskStatus("Pending"),
		Links:       []domain.TaskLink{},
		AgentType:   domain.AgentType("TestAgent"),
		FlowType:    domain.FlowType("TestFlow"),
		Created:     time.Now().UTC(),
		Updated:     time.Now().UTC(),
		FlowOptions: map[string]interface{}{"key": "value"},
	}

	err := db.AddTaskChange(context.Background(), taskRecord)
	assert.Nil(t, err)

	// Check that the task was added to the stream
	streamKey := fmt.Sprintf("%s:task_changes", workspaceId)
	streams, err := db.Client.XRange(context.Background(), streamKey, "-", "+").Result()
	assert.Nil(t, err)
	assert.NotNil(t, streams)
	assert.NotEmpty(t, streams)

	// Verify the values in the stream
	stream := streams[0] // Assuming the task is the first entry
	assert.Equal(t, taskRecord.Id, stream.Values["id"])
	assert.Equal(t, taskRecord.Title, stream.Values["title"])
	assert.Equal(t, taskRecord.Description, stream.Values["description"])
	assert.Equal(t, string(taskRecord.Status), stream.Values["status"])
	assert.Equal(t, string(taskRecord.AgentType), stream.Values["agentType"])
	assert.Equal(t, string(taskRecord.FlowType), stream.Values["flowType"])

	// Verify flowOptions field
	flowOptionsData, err := json.Marshal(taskRecord.FlowOptions)
	assert.Nil(t, err)
	assert.Equal(t, string(flowOptionsData), stream.Values["flowOptions"])
}
