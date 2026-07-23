package sqlite

import (
	"context"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProject(workspaceId, id, title string, priority domain.ProjectPriority, rank string) domain.Project {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return domain.Project{
		WorkspaceId: workspaceId,
		Id:          id,
		Title:       title,
		Description: "description of " + title,
		Priority:    priority,
		Rank:        rank,
		Created:     now,
		Updated:     now,
	}
}

func TestPersistProject(t *testing.T) {
	t.Parallel()
	storage := NewTestSqliteStorage(t, "project_test")
	ctx := context.Background()

	t.Run("insert new project", func(t *testing.T) {
		project := newTestProject("ws1", "project_1", "First Project", domain.ProjectPriorityMedium, "aa")

		err := storage.PersistProject(ctx, project)
		require.NoError(t, err)

		got, err := storage.GetProject(ctx, project.WorkspaceId, project.Id)
		require.NoError(t, err)
		assert.Equal(t, project, got)
	})

	t.Run("update existing project", func(t *testing.T) {
		project := newTestProject("ws1", "project_2", "Original Title", domain.ProjectPriorityNone, "")

		err := storage.PersistProject(ctx, project)
		require.NoError(t, err)

		project.Title = "Updated Title"
		project.Priority = domain.ProjectPriorityUrgent
		project.Rank = "bb"
		project.Updated = project.Updated.Add(time.Minute)

		err = storage.PersistProject(ctx, project)
		require.NoError(t, err)

		got, err := storage.GetProject(ctx, project.WorkspaceId, project.Id)
		require.NoError(t, err)
		assert.Equal(t, project, got)
	})
}

func TestGetProject(t *testing.T) {
	t.Parallel()
	storage := NewTestSqliteStorage(t, "project_test")
	ctx := context.Background()

	project := newTestProject("ws1", "project_1", "Some Project", domain.ProjectPriorityHigh, "aa")
	require.NoError(t, storage.PersistProject(ctx, project))

	t.Run("existing project", func(t *testing.T) {
		got, err := storage.GetProject(ctx, "ws1", "project_1")
		require.NoError(t, err)
		assert.Equal(t, project, got)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := storage.GetProject(ctx, "ws1", "project_missing")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("wrong workspace", func(t *testing.T) {
		_, err := storage.GetProject(ctx, "ws2", "project_1")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}

func TestGetProjects(t *testing.T) {
	t.Parallel()
	storage := NewTestSqliteStorage(t, "project_test")
	ctx := context.Background()

	t.Run("empty workspace", func(t *testing.T) {
		projects, err := storage.GetProjects(ctx, "ws_empty")
		require.NoError(t, err)
		assert.Empty(t, projects)
	})

	t.Run("sorted by priority bucket then rank", func(t *testing.T) {
		workspaceId := "ws_sorted"
		// persisted out of order on purpose
		projects := []domain.Project{
			newTestProject(workspaceId, "project_low", "Low", domain.ProjectPriorityLow, "aa"),
			newTestProject(workspaceId, "project_none", "None", domain.ProjectPriorityNone, "aa"),
			newTestProject(workspaceId, "project_urgent_b", "Urgent B", domain.ProjectPriorityUrgent, "bb"),
			newTestProject(workspaceId, "project_high", "High", domain.ProjectPriorityHigh, "aa"),
			newTestProject(workspaceId, "project_urgent_a", "Urgent A", domain.ProjectPriorityUrgent, "aa"),
			newTestProject(workspaceId, "project_medium", "Medium", domain.ProjectPriorityMedium, "aa"),
		}
		for _, p := range projects {
			require.NoError(t, storage.PersistProject(ctx, p))
		}

		got, err := storage.GetProjects(ctx, workspaceId)
		require.NoError(t, err)

		gotIds := make([]string, len(got))
		for i, p := range got {
			gotIds[i] = p.Id
		}
		assert.Equal(t, []string{
			"project_urgent_a",
			"project_urgent_b",
			"project_high",
			"project_medium",
			"project_low",
			"project_none",
		}, gotIds)
	})

	t.Run("excludes other workspaces", func(t *testing.T) {
		require.NoError(t, storage.PersistProject(ctx, newTestProject("ws_a", "project_a", "A", domain.ProjectPriorityNone, "")))
		require.NoError(t, storage.PersistProject(ctx, newTestProject("ws_b", "project_b", "B", domain.ProjectPriorityNone, "")))

		got, err := storage.GetProjects(ctx, "ws_a")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "project_a", got[0].Id)
	})
}

func TestDeleteProject(t *testing.T) {
	t.Parallel()
	storage := NewTestSqliteStorage(t, "project_test")
	ctx := context.Background()

	t.Run("existing project", func(t *testing.T) {
		project := newTestProject("ws1", "project_del", "To Delete", domain.ProjectPriorityLow, "")
		require.NoError(t, storage.PersistProject(ctx, project))

		err := storage.DeleteProject(ctx, project.WorkspaceId, project.Id)
		require.NoError(t, err)

		_, err = storage.GetProject(ctx, project.WorkspaceId, project.Id)
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("not found", func(t *testing.T) {
		err := storage.DeleteProject(ctx, "ws1", "project_missing")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}
