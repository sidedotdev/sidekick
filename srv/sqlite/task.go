package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"
	"strings"
	"time"
)

// Ensure Storage implements SubflowStorage interface
var _ domain.TaskStorage = (*Storage)(nil)

// PersistTask inserts or updates a Task in the SQLite database
func (s *Storage) PersistTask(ctx context.Context, task domain.Task) error {
	linksJSON, err := json.Marshal(task.Links)
	if err != nil {
		return fmt.Errorf("failed to marshal Links: %w", err)
	}

	flowOptionsJSON, err := json.Marshal(task.FlowOptions)
	if err != nil {
		return fmt.Errorf("failed to marshal FlowOptions: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO tasks (
			workspace_id, id, title, description, status, links, agent_type,
			flow_type, archived, created, updated, flow_options
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = s.db.ExecContext(ctx, query,
		task.WorkspaceId, task.Id, task.Title, task.Description, task.Status, linksJSON, task.AgentType,
		task.FlowType, task.Archived, task.Created, task.Updated, flowOptionsJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to persist task: %w", err)
	}

	return nil
}

// DeleteTask removes a Task from the SQLite database
func (s *Storage) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
	query := "DELETE FROM tasks WHERE workspace_id = ? AND id = ?"
	result, err := s.db.ExecContext(ctx, query, workspaceId, taskId)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return srv.ErrNotFound
	}

	return nil
}

// GetTask retrieves a single Task from the SQLite database
func (s *Storage) GetTask(ctx context.Context, workspaceId, taskId string) (domain.Task, error) {
	var task domain.Task
	var linksJSON, flowOptionsJSON []byte
	var archivedStr *string

	query := `SELECT workspace_id, id, title, description, status, links, agent_type, flow_type, archived, created, updated, flow_options
			  FROM tasks WHERE workspace_id = ? AND id = ?`
	err := s.db.QueryRowContext(ctx, query, workspaceId, taskId).Scan(
		&task.WorkspaceId, &task.Id, &task.Title, &task.Description, &task.Status,
		&linksJSON, &task.AgentType, &task.FlowType, &archivedStr,
		&task.Created, &task.Updated, &flowOptionsJSON)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Task{}, srv.ErrNotFound
		}
		return domain.Task{}, fmt.Errorf("failed to get task: %w", err)
	}

	if archivedStr != nil {
		archived, err := time.Parse(time.RFC3339, *archivedStr)
		if err != nil {
			return domain.Task{}, fmt.Errorf("failed to parse archived time: %w", err)
		}
		task.Archived = &archived
	}

	err = json.Unmarshal(linksJSON, &task.Links)
	if err != nil {
		return domain.Task{}, fmt.Errorf("failed to unmarshal links: %w", err)
	}

	err = json.Unmarshal(flowOptionsJSON, &task.FlowOptions)
	if err != nil {
		return domain.Task{}, fmt.Errorf("failed to unmarshal flow options: %w", err)
	}

	return task, nil
}

// GetTasks retrieves multiple Tasks from the SQLite database with optional status filtering
func (s *Storage) GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error) {
	query := `SELECT workspace_id, id, title, description, status, links, agent_type, flow_type, archived, created, updated, flow_options
			  FROM tasks WHERE workspace_id = ?`
	args := []interface{}{workspaceId}

	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i := range statuses {
			placeholders[i] = "?"
			args = append(args, statuses[i])
		}
		query += fmt.Sprintf(" AND status IN (%s)", strings.Join(placeholders, ","))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		var task domain.Task
		var linksJSON, flowOptionsJSON []byte
		var archivedStr *string

		err := rows.Scan(
			&task.WorkspaceId, &task.Id, &task.Title, &task.Description, &task.Status,
			&linksJSON, &task.AgentType, &task.FlowType, &archivedStr,
			&task.Created, &task.Updated, &flowOptionsJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task row: %w", err)
		}

		if archivedStr != nil {
			archived, err := time.Parse(time.RFC3339, *archivedStr)
			if err != nil {
				return nil, fmt.Errorf("failed to parse archived time: %w", err)
			}
			task.Archived = &archived
		}

		err = json.Unmarshal(linksJSON, &task.Links)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal links: %w", err)
		}

		err = json.Unmarshal(flowOptionsJSON, &task.FlowOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal flow options: %w", err)
		}

		tasks = append(tasks, task)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over task rows: %w", err)
	}

	return tasks, nil
}

// GetArchivedTasks retrieves archived tasks from the SQLite database with pagination
func (s *Storage) GetArchivedTasks(ctx context.Context, workspaceId string, offset, limit int64) ([]domain.Task, int64, error) {
	var totalCount int64
	countQuery := "SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND archived IS NOT NULL"
	err := s.db.QueryRowContext(ctx, countQuery, workspaceId).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count of archived tasks: %w", err)
	}

	query := `SELECT workspace_id, id, title, description, status, links, agent_type, flow_type, archived, created, updated, flow_options
			  FROM tasks WHERE workspace_id = ? AND archived IS NOT NULL ORDER BY archived DESC LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, query, workspaceId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query archived tasks: %w", err)
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		var task domain.Task
		var linksJSON, flowOptionsJSON []byte
		var archivedStr string

		err := rows.Scan(
			&task.WorkspaceId, &task.Id, &task.Title, &task.Description, &task.Status,
			&linksJSON, &task.AgentType, &task.FlowType, &archivedStr,
			&task.Created, &task.Updated, &flowOptionsJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan archived task row: %w", err)
		}

		archived, err := time.Parse(time.RFC3339, archivedStr)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to parse archived time: %w", err)
		}
		task.Archived = &archived

		err = json.Unmarshal(linksJSON, &task.Links)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal links: %w", err)
		}

		err = json.Unmarshal(flowOptionsJSON, &task.FlowOptions)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal flow options: %w", err)
		}

		tasks = append(tasks, task)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating over archived task rows: %w", err)
	}

	return tasks, totalCount, nil
}
