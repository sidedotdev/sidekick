package sqlite

import (
	"context"
	"encoding/json"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistTask(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	// Test inserting a new task
	task := domain.Task{
		WorkspaceId: "workspace1",
		Id:          "task1",
		Title:       "Test Task",
		Description: "This is a test task",
		Status:      domain.TaskStatusToDo,
		Links: []domain.TaskLink{
			{LinkType: "parent", TargetTaskId: "parent1"},
		},
		AgentType: domain.AgentTypeHuman,
		FlowType:  domain.FlowTypeBasicDev,
		Created:   time.Now().UTC(),
		Updated:   time.Now().UTC(),
		FlowOptions: map[string]interface{}{
			"option1": "value1",
		},
	}

	err := storage.PersistTask(ctx, task)
	assert.NoError(t, err)

	// Test updating an existing task
	task.Title = "Updated Test Task"
	task.Status = domain.TaskStatusInProgress
	task.Updated = time.Now().UTC()

	err = storage.PersistTask(ctx, task)
	assert.NoError(t, err)

	// Verify the task was updated, using GetTask
	var retrievedTask domain.Task
	var linksJSON, flowOptionsJSON []byte
	err = storage.db.QueryRowContext(ctx, "SELECT * FROM tasks WHERE workspace_id = ? AND id = ?", task.WorkspaceId, task.Id).Scan(
		&retrievedTask.WorkspaceId,
		&retrievedTask.Id,
		&retrievedTask.Title,
		&retrievedTask.Description,
		&retrievedTask.Status,
		&linksJSON,
		&retrievedTask.AgentType,
		&retrievedTask.FlowType,
		&retrievedTask.Archived,
		&retrievedTask.Created,
		&retrievedTask.Updated,
		&flowOptionsJSON,
	)
	require.NoError(t, err)

	err = json.Unmarshal(linksJSON, &retrievedTask.Links)
	require.NoError(t, err)

	err = json.Unmarshal(flowOptionsJSON, &retrievedTask.FlowOptions)
	require.NoError(t, err)

	assert.Equal(t, task.Title, retrievedTask.Title)
	assert.Equal(t, task.Status, retrievedTask.Status)
	assert.Equal(t, task.Links, retrievedTask.Links)
	assert.Equal(t, task.FlowOptions, retrievedTask.FlowOptions)
}

func TestDeleteTask(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")

	ctx := context.Background()

	// Insert a task to be deleted
	task := domain.Task{
		WorkspaceId: "workspace1",
		Id:          "task1",
		Title:       "Test Task",
		Status:      domain.TaskStatusToDo,
		Created:     time.Now().UTC(),
		Updated:     time.Now().UTC(),
	}

	err := storage.PersistTask(ctx, task)
	require.NoError(t, err)

	// Test deleting the task
	err = storage.DeleteTask(ctx, task.WorkspaceId, task.Id)
	assert.NoError(t, err)

	// Verify the task was deleted
	var count int
	err = storage.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND id = ?", task.WorkspaceId, task.Id).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Test deleting a non-existent task
	err = storage.DeleteTask(ctx, "nonexistent", "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
}
