package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/common"
	"sidekick/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var projectTracer = otel.Tracer("sidekick/srv/sqlite")

// Ensure Storage implements ProjectStorage interface
var _ domain.ProjectStorage = (*Storage)(nil)

// PersistProject inserts or updates a Project in the SQLite database
func (s *Storage) PersistProject(ctx context.Context, project domain.Project) error {
	ctx, span := projectTracer.Start(ctx, "Storage.PersistProject")
	defer span.End()
	span.SetAttributes(
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation", "INSERT"),
		attribute.String("workspace_id", project.WorkspaceId),
		attribute.String("project_id", project.Id),
	)

	query := `
		INSERT OR REPLACE INTO projects (
			workspace_id, id, title, description, priority, rank, created, updated
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	project.Created = project.Created.UTC()
	project.Updated = project.Updated.UTC()

	_, err := s.db.ExecContext(ctx, query,
		project.WorkspaceId, project.Id, project.Title, project.Description,
		project.Priority, project.Rank, project.Created, project.Updated,
	)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to persist project: %w", err)
	}

	return nil
}

// GetProject retrieves a single Project from the SQLite database
func (s *Storage) GetProject(ctx context.Context, workspaceId, projectId string) (domain.Project, error) {
	ctx, span := projectTracer.Start(ctx, "Storage.GetProject")
	defer span.End()
	span.SetAttributes(
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation", "SELECT"),
		attribute.String("workspace_id", workspaceId),
		attribute.String("project_id", projectId),
	)

	var project domain.Project
	query := `SELECT workspace_id, id, title, description, priority, rank, created, updated
			  FROM projects WHERE workspace_id = ? AND id = ?`
	err := s.db.QueryRowContext(ctx, query, workspaceId, projectId).Scan(
		&project.WorkspaceId, &project.Id, &project.Title, &project.Description,
		&project.Priority, &project.Rank, &project.Created, &project.Updated)

	if err != nil {
		if err == sql.ErrNoRows {
			span.RecordError(common.ErrNotFound)
			span.SetStatus(codes.Error, common.ErrNotFound.Error())
			return domain.Project{}, common.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return domain.Project{}, fmt.Errorf("failed to get project: %w", err)
	}

	return project, nil
}

// GetProjects retrieves all Projects in a workspace from the SQLite database,
// sorted by priority bucket (most urgent first) then rank.
func (s *Storage) GetProjects(ctx context.Context, workspaceId string) ([]domain.Project, error) {
	ctx, span := projectTracer.Start(ctx, "Storage.GetProjects")
	defer span.End()
	span.SetAttributes(
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation", "SELECT"),
		attribute.String("workspace_id", workspaceId),
	)

	query := `SELECT workspace_id, id, title, description, priority, rank, created, updated
			  FROM projects WHERE workspace_id = ?
			  ORDER BY
				CASE priority
					WHEN 'urgent' THEN 0
					WHEN 'high' THEN 1
					WHEN 'medium' THEN 2
					WHEN 'low' THEN 3
					ELSE 4
				END,
				rank,
				created`

	rows, err := s.db.QueryContext(ctx, query, workspaceId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		var project domain.Project
		err := rows.Scan(
			&project.WorkspaceId, &project.Id, &project.Title, &project.Description,
			&project.Priority, &project.Rank, &project.Created, &project.Updated)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("failed to scan project row: %w", err)
		}
		projects = append(projects, project)
	}

	if err = rows.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("error iterating over project rows: %w", err)
	}

	return projects, nil
}

// DeleteProject removes a Project from the SQLite database
func (s *Storage) DeleteProject(ctx context.Context, workspaceId, projectId string) error {
	ctx, span := projectTracer.Start(ctx, "Storage.DeleteProject")
	defer span.End()
	span.SetAttributes(
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation", "DELETE"),
		attribute.String("workspace_id", workspaceId),
		attribute.String("project_id", projectId),
	)

	query := "DELETE FROM projects WHERE workspace_id = ? AND id = ?"
	result, err := s.db.ExecContext(ctx, query, workspaceId, projectId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to delete project: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		span.RecordError(common.ErrNotFound)
		span.SetStatus(codes.Error, common.ErrNotFound.Error())
		return common.ErrNotFound
	}

	return nil
}
