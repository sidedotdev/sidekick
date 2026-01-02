package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"time"
)

// Ensure Storage implements FlowStorage interface
var _ domain.FlowStorage = (*Storage)(nil)

func (s *Storage) PersistFlow(ctx context.Context, flow domain.Flow) error {
	now := time.Now().UTC()
	if flow.Created.IsZero() {
		flow.Created = now
	} else {
		flow.Created = flow.Created.UTC()
	}
	if flow.Updated.IsZero() {
		flow.Updated = now
	} else {
		flow.Updated = flow.Updated.UTC()
	}

	query := `
		INSERT OR REPLACE INTO flows (workspace_id, id, type, parent_id, status, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		flow.WorkspaceId, flow.Id, flow.Type, flow.ParentId, flow.Status,
		flow.Created.Format(time.RFC3339Nano), flow.Updated.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to persist flow: %w", err)
	}

	return nil
}

func (s *Storage) GetFlow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error) {
	query := `
		SELECT workspace_id, id, type, parent_id, status, created, updated
		FROM flows
		WHERE workspace_id = ? AND id = ?
	`

	var flow domain.Flow
	var createdStr, updatedStr string
	err := s.db.QueryRowContext(ctx, query, workspaceId, flowId).Scan(
		&flow.WorkspaceId, &flow.Id, &flow.Type, &flow.ParentId, &flow.Status,
		&createdStr, &updatedStr)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Flow{}, common.ErrNotFound
		}
		return domain.Flow{}, fmt.Errorf("failed to get flow: %w", err)
	}

	var parseErr error
	flow.Created, parseErr = time.Parse(time.RFC3339Nano, createdStr)
	if parseErr != nil {
		return domain.Flow{}, fmt.Errorf("failed to parse created timestamp: %w", parseErr)
	}
	flow.Updated, parseErr = time.Parse(time.RFC3339Nano, updatedStr)
	if parseErr != nil {
		return domain.Flow{}, fmt.Errorf("failed to parse updated timestamp: %w", parseErr)
	}

	return flow, nil
}

func (s *Storage) GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error) {
	query := `
		SELECT workspace_id, id, type, parent_id, status, created, updated
		FROM flows
		WHERE workspace_id = ? AND parent_id = ?
	`

	rows, err := s.db.QueryContext(ctx, query, workspaceId, taskId)
	if err != nil {
		return nil, fmt.Errorf("failed to query flows for task: %w", err)
	}
	defer rows.Close()

	flows := make([]domain.Flow, 0)
	for rows.Next() {
		var flow domain.Flow
		var createdStr, updatedStr string
		err := rows.Scan(&flow.WorkspaceId, &flow.Id, &flow.Type, &flow.ParentId, &flow.Status,
			&createdStr, &updatedStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan flow row: %w", err)
		}
		flow.Created, err = time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse created timestamp: %w", err)
		}
		flow.Updated, err = time.Parse(time.RFC3339Nano, updatedStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse updated timestamp: %w", err)
		}
		flows = append(flows, flow)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating flow rows: %w", err)
	}

	return flows, nil
}
