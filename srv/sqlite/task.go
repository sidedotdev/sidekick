package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
)

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
		INSERT INTO tasks (
			workspace_id, id, title, description, status, links, agent_type,
			flow_type, archived, created, updated, flow_options
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, id) DO UPDATE SET
			title = ?, description = ?, status = ?, links = ?, agent_type = ?,
			flow_type = ?, archived = ?, updated = ?, flow_options = ?
	`

	_, err = s.db.ExecContext(ctx, query,
		task.WorkspaceId, task.Id, task.Title, task.Description, task.Status, linksJSON, task.AgentType,
		task.FlowType, task.Archived, task.Created, task.Updated, flowOptionsJSON,
		task.Title, task.Description, task.Status, linksJSON, task.AgentType,
		task.FlowType, task.Archived, task.Updated, flowOptionsJSON,
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
		return fmt.Errorf("task not found")
	}

	return nil
}
