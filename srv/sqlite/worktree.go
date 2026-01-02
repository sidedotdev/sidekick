package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"time"
)

func (s *Storage) PersistWorktree(ctx context.Context, worktree domain.Worktree) error {
	if worktree.Created.IsZero() {
		worktree.Created = time.Now().UTC()
	} else {
		worktree.Created = worktree.Created.UTC()
	}

	query := `
		INSERT OR REPLACE INTO worktrees (id, flow_id, name, created, workspace_id, working_directory)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		worktree.Id,
		worktree.FlowId,
		worktree.Name,
		worktree.Created.Format(time.RFC3339Nano),
		worktree.WorkspaceId,
		worktree.WorkingDirectory,
	)
	if err != nil {
		return fmt.Errorf("failed to persist worktree: %w", err)
	}

	return nil
}

func (s *Storage) GetWorktree(ctx context.Context, workspaceId, worktreeId string) (domain.Worktree, error) {
	query := `
		SELECT id, flow_id, name, created, workspace_id, working_directory
		FROM worktrees
		WHERE workspace_id = ? AND id = ?
	`

	var worktree domain.Worktree
	var createdStr string
	err := s.db.QueryRowContext(ctx, query, workspaceId, worktreeId).Scan(
		&worktree.Id,
		&worktree.FlowId,
		&worktree.Name,
		&createdStr,
		&worktree.WorkspaceId,
		&worktree.WorkingDirectory,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Worktree{}, common.ErrNotFound
		}
		return domain.Worktree{}, fmt.Errorf("failed to get worktree: %w", err)
	}

	worktree.Created, err = time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return domain.Worktree{}, fmt.Errorf("failed to parse created timestamp: %w", err)
	}

	return worktree, nil
}

func (s *Storage) GetWorktrees(ctx context.Context, workspaceId string) ([]domain.Worktree, error) {
	query := `
		SELECT id, flow_id, name, created, workspace_id, working_directory
		FROM worktrees
		WHERE workspace_id = ?
	`
	rows, err := s.db.QueryContext(ctx, query, workspaceId)
	if err != nil {
		return nil, fmt.Errorf("failed to query worktrees: %w", err)
	}
	defer rows.Close()
	return s.getWorktreesFromRows(rows)
}

func (s Storage) GetWorktreesForFlow(ctx context.Context, workspaceId, flowId string) ([]domain.Worktree, error) {
	query := `
		SELECT id, flow_id, name, created, workspace_id, working_directory
		FROM worktrees
		WHERE workspace_id = ? AND flow_id = ?
	`
	rows, err := s.db.QueryContext(ctx, query, workspaceId, flowId)
	if err != nil {
		return nil, fmt.Errorf("failed to query worktrees: %w", err)
	}
	defer rows.Close()
	return s.getWorktreesFromRows(rows)
}

func (s Storage) getWorktreesFromRows(rows *sql.Rows) ([]domain.Worktree, error) {
	var worktrees []domain.Worktree
	for rows.Next() {
		var worktree domain.Worktree
		var createdStr string
		err := rows.Scan(
			&worktree.Id,
			&worktree.FlowId,
			&worktree.Name,
			&createdStr,
			&worktree.WorkspaceId,
			&worktree.WorkingDirectory,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan worktree: %w", err)
		}
		worktree.Created, err = time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse created timestamp: %w", err)
		}
		worktrees = append(worktrees, worktree)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating worktrees: %w", err)
	}

	return worktrees, nil
}

func (s *Storage) DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error {
	query := `
		DELETE FROM worktrees
		WHERE workspace_id = ? AND id = ?
	`

	result, err := s.db.ExecContext(ctx, query, workspaceId, worktreeId)
	if err != nil {
		return fmt.Errorf("failed to delete worktree: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return common.ErrNotFound
	}

	return nil
}
