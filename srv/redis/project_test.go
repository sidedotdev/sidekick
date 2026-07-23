package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sidekick/domain"
	"sidekick/srv"

	"github.com/segmentio/ksuid"
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
	db := newTestRedisStorage(t)
	ctx := context.Background()

	validProject := newTestProject(ksuid.New().String(), "project_"+ksuid.New().String(), "Test Project", domain.ProjectPriorityMedium, "aa")

	tests := []struct {
		name          string
		project       domain.Project
		expectedError bool
		errorContains string
	}{
		{
			name:    "successfully persist a valid project",
			project: validProject,
		},
		{
			name: "empty WorkspaceId",
			project: func() domain.Project {
				p := validProject
				p.WorkspaceId = ""
				return p
			}(),
			expectedError: true,
			errorContains: "workspaceId",
		},
		{
			name: "empty Id",
			project: func() domain.Project {
				p := validProject
				p.Id = ""
				return p
			}(),
			expectedError: true,
			errorContains: "project.Id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.PersistProject(ctx, tt.project)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)

				projectKey := fmt.Sprintf("%s:%s", tt.project.WorkspaceId, tt.project.Id)
				exists, err := db.Client.Exists(ctx, projectKey).Result()
				require.NoError(t, err)
				assert.Equal(t, int64(1), exists)

				isMember, err := db.Client.SIsMember(ctx, fmt.Sprintf("%s:projects", tt.project.WorkspaceId), tt.project.Id).Result()
				require.NoError(t, err)
				assert.True(t, isMember)
			}
		})
	}

	t.Run("update existing project", func(t *testing.T) {
		updated := validProject
		updated.Title = "Updated Title"
		updated.Priority = domain.ProjectPriorityUrgent
		require.NoError(t, db.PersistProject(ctx, updated))

		got, err := db.GetProject(ctx, updated.WorkspaceId, updated.Id)
		require.NoError(t, err)
		assert.Equal(t, updated, got)
	})
}

func TestGetProject(t *testing.T) {
	db := newTestRedisStorage(t)
	ctx := context.Background()

	project := newTestProject(ksuid.New().String(), "project_"+ksuid.New().String(), "Test Project", domain.ProjectPriorityHigh, "aa")
	require.NoError(t, db.PersistProject(ctx, project))

	t.Run("existing project", func(t *testing.T) {
		got, err := db.GetProject(ctx, project.WorkspaceId, project.Id)
		require.NoError(t, err)
		assert.Equal(t, project, got)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := db.GetProject(ctx, project.WorkspaceId, "project_missing")
		assert.ErrorIs(t, err, srv.ErrNotFound)
	})
}

func TestGetProjects(t *testing.T) {
	db := newTestRedisStorage(t)
	ctx := context.Background()

	t.Run("empty workspace", func(t *testing.T) {
		projects, err := db.GetProjects(ctx, "ws_empty")
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
			require.NoError(t, db.PersistProject(ctx, p))
		}

		got, err := db.GetProjects(ctx, workspaceId)
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
		require.NoError(t, db.PersistProject(ctx, newTestProject("ws_a", "project_a", "A", domain.ProjectPriorityNone, "")))
		require.NoError(t, db.PersistProject(ctx, newTestProject("ws_b", "project_b", "B", domain.ProjectPriorityNone, "")))

		got, err := db.GetProjects(ctx, "ws_a")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "project_a", got[0].Id)
	})
}

func TestDeleteProject(t *testing.T) {
	db := newTestRedisStorage(t)
	ctx := context.Background()

	project := newTestProject(ksuid.New().String(), "project_"+ksuid.New().String(), "Test Project", domain.ProjectPriorityLow, "aa")
	require.NoError(t, db.PersistProject(ctx, project))

	t.Run("delete existing project", func(t *testing.T) {
		require.NoError(t, db.DeleteProject(ctx, project.WorkspaceId, project.Id))

		_, err := db.GetProject(ctx, project.WorkspaceId, project.Id)
		assert.ErrorIs(t, err, srv.ErrNotFound)

		isMember, err := db.Client.SIsMember(ctx, fmt.Sprintf("%s:projects", project.WorkspaceId), project.Id).Result()
		require.NoError(t, err)
		assert.False(t, isMember)
	})

	t.Run("not found", func(t *testing.T) {
		err := db.DeleteProject(ctx, project.WorkspaceId, "project_missing")
		assert.ErrorIs(t, err, srv.ErrNotFound)
	})
}
