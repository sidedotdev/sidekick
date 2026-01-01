package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"testing"
	"time"

	go_redis "github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
)

func TestPersistTask(t *testing.T) {
	db := newTestRedisStorage()

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

	// Verify that the task is in the archived sorted set
	archivedKey := fmt.Sprintf("%s:archived_tasks", taskRecord.WorkspaceId)
	score, err := db.Client.ZScore(context.Background(), archivedKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.Equal(t, float64(now.Unix()), score)

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

	// Verify that the task is not in the archived sorted set
	_, err = db.Client.ZScore(context.Background(), archivedKey, taskRecord.Id).Result()
	assert.Equal(t, go_redis.Nil, err)

	// Verify that the task is in the correct kanban set
	statusKey = fmt.Sprintf("%s:kanban:%s", taskRecord.WorkspaceId, taskRecord.Status)
	isMember, err = db.Client.SIsMember(context.Background(), statusKey, taskRecord.Id).Result()
	assert.Nil(t, err)
	assert.True(t, isMember)
}

func TestGetTasks(t *testing.T) {
	db := newTestRedisStorage()
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
	db := newTestRedisStorage()
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

func TestGetArchivedTasks(t *testing.T) {
	db := newTestRedisStorage()
	ctx := context.Background()

	workspaceId := "test-workspace"

	// Create and persist some archived tasks
	archivedTasks := []domain.Task{
		{
			Id:          "archived-task-1",
			WorkspaceId: workspaceId,
			Title:       "Archived Task 1",
			Status:      domain.TaskStatusComplete,
			Archived:    func() *time.Time { t := time.Now().Add(-2 * time.Hour); return &t }(),
		},
		{
			Id:          "archived-task-2",
			WorkspaceId: workspaceId,
			Title:       "Archived Task 2",
			Status:      domain.TaskStatusComplete,
			Archived:    func() *time.Time { t := time.Now().Add(-1 * time.Hour); return &t }(),
		},
		{
			Id:          "archived-task-3",
			WorkspaceId: workspaceId,
			Title:       "Archived Task 3",
			Status:      domain.TaskStatusComplete,
			Archived:    func() *time.Time { t := time.Now(); return &t }(),
		},
	}

	for _, task := range archivedTasks {
		err := db.PersistTask(ctx, task)
		assert.NoError(t, err)
	}

	// Test getting all archived tasks
	retrievedTasks, totalCount, err := db.GetArchivedTasks(ctx, workspaceId, 1, 3)
	assert.NoError(t, err)
	assert.Len(t, retrievedTasks, 3)
	assert.Equal(t, totalCount, int64(3))

	// Check if tasks are in the correct order (most recent first)
	assert.Equal(t, "archived-task-3", retrievedTasks[0].Id)
	assert.Equal(t, "archived-task-2", retrievedTasks[1].Id)
	assert.Equal(t, "archived-task-1", retrievedTasks[2].Id)

	// Test pagination
	retrievedTasks, totalCount, err = db.GetArchivedTasks(ctx, workspaceId, 2, 2)
	assert.NoError(t, err)
	assert.Len(t, retrievedTasks, 1)
	assert.Equal(t, "archived-task-1", retrievedTasks[0].Id)
	assert.Equal(t, totalCount, int64(3))

	// Test getting archived tasks from a workspace with no archived tasks
	emptyWorkspaceId := "empty-workspace-id"
	emptyTasks, totalCount, err := db.GetArchivedTasks(ctx, emptyWorkspaceId, 1, 10)
	assert.NoError(t, err)
	assert.Len(t, emptyTasks, 0)
	assert.Equal(t, totalCount, int64(0))
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

func TestStreamTaskChanges(t *testing.T) {
	db := NewTestRedisStreamer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workspaceId := "TEST_WORKSPACE_ID"

	taskChan, errChan := db.StreamTaskChanges(ctx, workspaceId, "")

	// Add a task change
	task1 := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_1",
		Title:       "Test Task 1",
		Status:      domain.TaskStatusToDo,
	}
	go func() {
		time.Sleep(1 * time.Millisecond)
		err := db.AddTaskChange(ctx, task1)
		assert.NoError(t, err)
	}()

	// Check if the task is received through the channel
	select {
	case receivedTask := <-taskChan:
		assert.Equal(t, task1.Id, receivedTask.Id)
		assert.Equal(t, task1.Title, receivedTask.Title)
		assert.Equal(t, task1.Status, receivedTask.Status)
	case err := <-errChan:
		t.Fatalf("Received unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for task")
	}

	// Add another task change
	task2 := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_2",
		Title:       "Test Task 2",
		Status:      domain.TaskStatusInProgress,
	}
	go func() {
		time.Sleep(1 * time.Millisecond)
		err := db.AddTaskChange(ctx, task2)
		assert.NoError(t, err)
	}()

	// Check if the second task is received through the channel
	select {
	case receivedTask := <-taskChan:
		assert.Equal(t, task2.Id, receivedTask.Id)
		assert.Equal(t, task2.Title, receivedTask.Title)
		assert.Equal(t, task2.Status, receivedTask.Status)
	case err := <-errChan:
		t.Fatalf("Received unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for task")
	}

	// Test context cancellation
	cancel()
	select {
	case _, ok := <-taskChan:
		assert.False(t, ok, "Task channel should be closed after context cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for task channel to close")
	}

	select {
	case _, ok := <-errChan:
		assert.False(t, ok, "Error channel should be closed after context cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for error channel to close")
	}
}

func TestStreamTaskChanges_TimestampUTC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamer := NewStreamer()
	defer streamer.Client.Close()

	workspaceId := "test-workspace-" + ksuid.New().String()

	// Clear the stream used by this test
	streamKey := fmt.Sprintf("%s:task_changes", workspaceId)
	if err := streamer.Client.Del(ctx, streamKey).Err(); err != nil {
		t.Fatalf("Failed to clear test stream: %v", err)
	}

	// Use non-UTC timezone with nanosecond precision
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("Failed to load timezone: %v", err)
	}
	baseTime := time.Date(2025, 6, 15, 10, 30, 45, 123456789, loc)

	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task-" + ksuid.New().String(),
		Title:       "Test Task",
		Status:      domain.TaskStatusToDo,
		Created:     baseTime,
		Updated:     baseTime.Add(time.Hour),
	}

	taskChan, errChan := streamer.StreamTaskChanges(ctx, workspaceId, "0")

	// Add task change
	if err := streamer.AddTaskChange(ctx, task); err != nil {
		t.Fatalf("Failed to add task change: %v", err)
	}

	select {
	case received := <-taskChan:
		if received.Id != task.Id {
			t.Errorf("Task ID mismatch: got %s, want %s", received.Id, task.Id)
		}
		// Verify timestamps are in UTC
		if received.Created.Location() != time.UTC {
			t.Errorf("Created not in UTC: got location %v", received.Created.Location())
		}
		if received.Updated.Location() != time.UTC {
			t.Errorf("Updated not in UTC: got location %v", received.Updated.Location())
		}
		// Verify time values are equivalent
		if !received.Created.Equal(task.Created) {
			t.Errorf("Created time mismatch: got %v, want %v", received.Created, task.Created)
		}
		if !received.Updated.Equal(task.Updated) {
			t.Errorf("Updated time mismatch: got %v, want %v", received.Updated, task.Updated)
		}
	case err := <-errChan:
		t.Fatalf("Received error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for task")
	}
}
