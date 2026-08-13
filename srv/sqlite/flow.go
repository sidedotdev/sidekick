package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"time"
)

// Rows persisted before the metadata column existed, as well as flows without
// metadata, hold SQL NULL, so an absent value is not an error.
func unmarshalFlowMetadata(metadataJSON []byte, flow *domain.Flow) error {
	if len(metadataJSON) == 0 || string(metadataJSON) == "null" {
		return nil
	}
	if err := json.Unmarshal(metadataJSON, &flow.Metadata); err != nil {
		return fmt.Errorf("failed to unmarshal flow metadata: %w", err)
	}
	return nil
}

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

	var metadataJSON []byte
	if flow.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(flow.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal flow metadata: %w", err)
		}
	}

	// Upsert via ON CONFLICT (rather than INSERT OR REPLACE) so updates keep
	// the original created timestamp and rowid, keeping creation-order
	// listings stable.
	query := `
		INSERT INTO flows (workspace_id, id, type, parent_id, status, title, metadata, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			type = excluded.type,
			parent_id = excluded.parent_id,
			status = excluded.status,
			title = excluded.title,
			metadata = excluded.metadata,
			updated = excluded.updated
	`

	_, err := s.db.ExecContext(ctx, query,
		flow.WorkspaceId, flow.Id, flow.Type, flow.ParentId, flow.Status, flow.Title, metadataJSON,
		flow.Created.Format(time.RFC3339Nano), flow.Updated.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to persist flow: %w", err)
	}

	return nil
}

func (s *Storage) GetFlow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error) {
	query := `
		SELECT workspace_id, id, type, parent_id, status, title, metadata, created, updated
		FROM flows
		WHERE workspace_id = ? AND id = ?
	`

	var flow domain.Flow
	var createdStr, updatedStr string
	var metadataJSON []byte
	err := s.db.QueryRowContext(ctx, query, workspaceId, flowId).Scan(
		&flow.WorkspaceId, &flow.Id, &flow.Type, &flow.ParentId, &flow.Status, &flow.Title, &metadataJSON,
		&createdStr, &updatedStr)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Flow{}, common.ErrNotFound
		}
		return domain.Flow{}, fmt.Errorf("failed to get flow: %w", err)
	}

	if err := unmarshalFlowMetadata(metadataJSON, &flow); err != nil {
		return domain.Flow{}, err
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
		SELECT workspace_id, id, type, parent_id, status, title, metadata, created, updated
		FROM flows
		WHERE workspace_id = ? AND parent_id = ?
		ORDER BY created ASC, id ASC
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
		var metadataJSON []byte
		err := rows.Scan(&flow.WorkspaceId, &flow.Id, &flow.Type, &flow.ParentId, &flow.Status,
			&flow.Title, &metadataJSON, &createdStr, &updatedStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan flow row: %w", err)
		}
		if err := unmarshalFlowMetadata(metadataJSON, &flow); err != nil {
			return nil, err
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

func (s *Storage) DeleteFlow(ctx context.Context, workspaceId, flowId string) error {
	query := "DELETE FROM flows WHERE workspace_id = ? AND id = ?"
	_, err := s.db.ExecContext(ctx, query, workspaceId, flowId)
	if err != nil {
		return fmt.Errorf("failed to delete flow %s: %w", flowId, err)
	}
	return nil
}
