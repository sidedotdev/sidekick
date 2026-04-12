package sqlite

import (
	"context"
	"sidekick/common"
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

	// Verify the task was updated
	retrievedTask, err := storage.GetTask(ctx, task.WorkspaceId, task.Id)
	assert.NoError(t, err)
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
	_, err = storage.GetTask(ctx, task.WorkspaceId, task.Id)
	require.Error(t, err)
	assert.Equal(t, common.ErrNotFound, err)

	// Test deleting a non-existent task
	err = storage.DeleteTask(ctx, "nonexistent", "nonexistent")
	assert.Error(t, err)
	assert.Equal(t, common.ErrNotFound.Error(), err.Error())
}

func TestGetTask(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	// Create a task
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
	require.NoError(t, err)

	// Test getting the task
	retrievedTask, err := storage.GetTask(ctx, task.WorkspaceId, task.Id)
	assert.NoError(t, err)
	assert.Equal(t, task.WorkspaceId, retrievedTask.WorkspaceId)
	assert.Equal(t, task.Id, retrievedTask.Id)
	assert.Equal(t, task.Title, retrievedTask.Title)
	assert.Equal(t, task.Description, retrievedTask.Description)
	assert.Equal(t, task.Status, retrievedTask.Status)
	assert.Equal(t, task.Links, retrievedTask.Links)
	assert.Equal(t, task.AgentType, retrievedTask.AgentType)
	assert.Equal(t, task.FlowType, retrievedTask.FlowType)
	assert.Equal(t, task.Created, retrievedTask.Created)
	assert.Equal(t, task.Updated, retrievedTask.Updated)
	assert.Equal(t, task.FlowOptions, retrievedTask.FlowOptions)

	// Test getting a non-existent task
	_, err = storage.GetTask(ctx, "nonexistent", "nonexistent")
	assert.Error(t, err)
	assert.Equal(t, common.ErrNotFound, err)
}

func TestGetTasks(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	// Create multiple tasks
	tasks := []domain.Task{
		{
			WorkspaceId: "workspace1",
			Id:          "task1",
			Title:       "Task 1",
			Status:      domain.TaskStatusToDo,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		},
		{
			WorkspaceId: "workspace1",
			Id:          "task2",
			Title:       "Task 2",
			Status:      domain.TaskStatusInProgress,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		},
		{
			WorkspaceId: "workspace1",
			Id:          "task3",
			Title:       "Task 3",
			Status:      domain.TaskStatusComplete,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		},
	}

	for _, task := range tasks {
		err := storage.PersistTask(ctx, task)
		require.NoError(t, err)
	}

	// Test getting all tasks
	retrievedTasks, err := storage.GetTasks(ctx, "workspace1", nil)
	assert.NoError(t, err)
	assert.Len(t, retrievedTasks, 3)

	// Test filtering by status
	filteredTasks, err := storage.GetTasks(ctx, "workspace1", []domain.TaskStatus{domain.TaskStatusToDo, domain.TaskStatusInProgress})
	assert.NoError(t, err)
	assert.Len(t, filteredTasks, 2)

	// Test getting tasks from non-existent workspace
	emptyTasks, err := storage.GetTasks(ctx, "nonexistent", nil)
	assert.NoError(t, err)
	assert.Len(t, emptyTasks, 0)
}

func TestGetArchivedTasks(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	// Create multiple tasks, some archived
	now := time.Now().UTC()
	archivedTime := now.Add(-24 * time.Hour)
	tasks := []domain.Task{
		{
			WorkspaceId: "workspace1",
			Id:          "task1",
			Title:       "Task 1",
			Status:      domain.TaskStatusComplete,
			Created:     now,
			Updated:     now,
			Archived:    &archivedTime,
		},
		{
			WorkspaceId: "workspace1",
			Id:          "task2",
			Title:       "Task 2",
			Status:      domain.TaskStatusComplete,
			Created:     now,
			Updated:     now,
			Archived:    &archivedTime,
		},
		{
			WorkspaceId: "workspace1",
			Id:          "task3",
			Title:       "Task 3",
			Status:      domain.TaskStatusInProgress,
			Created:     now,
			Updated:     now,
		},
	}

	for _, task := range tasks {
		err := storage.PersistTask(ctx, task)
		require.NoError(t, err)
	}

	// Test getting archived tasks
	archivedTasks, totalCount, err := storage.GetArchivedTasks(ctx, "workspace1", 1, 10)
	assert.NoError(t, err)
	assert.Len(t, archivedTasks, 2)
	assert.Equal(t, int64(2), totalCount)

	// Test pagination
	archivedTasks, totalCount, err = storage.GetArchivedTasks(ctx, "workspace1", 2, 1)
	assert.NoError(t, err)
	assert.Len(t, archivedTasks, 1)
	assert.Equal(t, int64(2), totalCount)

	// Test getting archived tasks from non-existent workspace
	emptyTasks, totalCount, err := storage.GetArchivedTasks(ctx, "nonexistent", 1, 10)
	assert.NoError(t, err)
	assert.Len(t, emptyTasks, 0)
	assert.Equal(t, int64(0), totalCount)
}

func TestUpdateTaskTitle(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	t.Run("sets title when empty", func(t *testing.T) {
		task := domain.Task{
			WorkspaceId: "ws1",
			Id:          "task_empty_title",
			Description: "Fix the login bug",
			Title:       "",
			Status:      domain.TaskStatusToDo,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		}
		require.NoError(t, storage.PersistTask(ctx, task))

		err := storage.UpdateTaskTitle(ctx, "ws1", "task_empty_title", "Login Bug Fix")
		require.NoError(t, err)

		got, err := storage.GetTask(ctx, "ws1", "task_empty_title")
		require.NoError(t, err)
		assert.Equal(t, "Login Bug Fix", got.Title)
	})

	t.Run("sets title when it matches description", func(t *testing.T) {
		task := domain.Task{
			WorkspaceId: "ws1",
			Id:          "task_desc_title",
			Description: "Fix the login bug",
			Title:       "Fix the login bug",
			Status:      domain.TaskStatusToDo,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		}
		require.NoError(t, storage.PersistTask(ctx, task))

		err := storage.UpdateTaskTitle(ctx, "ws1", "task_desc_title", "Login Bug Fix")
		require.NoError(t, err)

		got, err := storage.GetTask(ctx, "ws1", "task_desc_title")
		require.NoError(t, err)
		assert.Equal(t, "Login Bug Fix", got.Title)
	})

	t.Run("does not overwrite manually set title", func(t *testing.T) {
		task := domain.Task{
			WorkspaceId: "ws1",
			Id:          "task_manual_title",
			Description: "Fix the login bug",
			Title:       "My Custom Title",
			Status:      domain.TaskStatusToDo,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		}
		require.NoError(t, storage.PersistTask(ctx, task))

		err := storage.UpdateTaskTitle(ctx, "ws1", "task_manual_title", "Generated Title")
		require.NoError(t, err)

		got, err := storage.GetTask(ctx, "ws1", "task_manual_title")
		require.NoError(t, err)
		assert.Equal(t, "My Custom Title", got.Title)
	})
}

func TestUpdateTaskStatus(t *testing.T) {
	storage := NewTestSqliteStorage(t, "task_test")
	ctx := context.Background()

	t.Run("updates status and agent_type without clobbering title", func(t *testing.T) {
		task := domain.Task{
			WorkspaceId: "ws1",
			Id:          "task_status_update",
			Title:       "My Title",
			Description: "Do something",
			Status:      domain.TaskStatusToDo,
			AgentType:   domain.AgentTypeLLM,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		}
		require.NoError(t, storage.PersistTask(ctx, task))

		err := storage.UpdateTaskStatus(ctx, "ws1", "task_status_update", domain.TaskStatusBlocked, domain.AgentTypeHuman)
		require.NoError(t, err)

		got, err := storage.GetTask(ctx, "ws1", "task_status_update")
		require.NoError(t, err)
		assert.Equal(t, domain.TaskStatusBlocked, got.Status)
		assert.Equal(t, domain.AgentTypeHuman, got.AgentType)
		assert.Equal(t, "My Title", got.Title)
		assert.True(t, got.Updated.After(task.Updated) || got.Updated.Equal(task.Updated))
	})

	t.Run("updates status to complete", func(t *testing.T) {
		task := domain.Task{
			WorkspaceId: "ws1",
			Id:          "task_complete_update",
			Title:       "Another Title",
			Description: "Finish this",
			Status:      domain.TaskStatusInProgress,
			AgentType:   domain.AgentTypeLLM,
			Created:     time.Now().UTC(),
			Updated:     time.Now().UTC(),
		}
		require.NoError(t, storage.PersistTask(ctx, task))

		err := storage.UpdateTaskStatus(ctx, "ws1", "task_complete_update", domain.TaskStatusComplete, domain.AgentTypeNone)
		require.NoError(t, err)

		got, err := storage.GetTask(ctx, "ws1", "task_complete_update")
		require.NoError(t, err)
		assert.Equal(t, domain.TaskStatusComplete, got.Status)
		assert.Equal(t, domain.AgentTypeNone, got.AgentType)
		assert.Equal(t, "Another Title", got.Title)
	})
}
