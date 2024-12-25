package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/domain"

	"github.com/rs/zerolog/log"
)

// Ensure Storage implements SubflowStorage interface
var _ domain.SubflowStorage = (*Storage)(nil)

func (s *Storage) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	if subflow.WorkspaceId == "" || subflow.Id == "" || subflow.FlowId == "" {
		return errors.New("workspaceId, subflow.Id, and subflow.FlowId cannot be empty")
	}

	query := `
		INSERT OR REPLACE INTO subflows (
			id, workspace_id, name, description, status, parent_subflow_id, flow_id, result
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := json.Marshal(subflow.Result)
	if err != nil {
		return fmt.Errorf("failed to marshal subflow result: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query,
		subflow.Id,
		subflow.WorkspaceId,
		subflow.Name,
		subflow.Description,
		subflow.Status,
		subflow.ParentSubflowId,
		subflow.FlowId,
		result,
	)

	if err != nil {
		log.Error().Err(err).
			Str("subflowId", subflow.Id).
			Str("workspaceId", subflow.WorkspaceId).
			Str("flowId", subflow.FlowId).
			Msg("Failed to persist subflow")
		return fmt.Errorf("failed to persist subflow: %w", err)
	}

	log.Debug().
		Str("subflowId", subflow.Id).
		Str("workspaceId", subflow.WorkspaceId).
		Str("flowId", subflow.FlowId).
		Msg("Subflow persisted successfully")

	return nil
}

func (s *Storage) GetSubflows(ctx context.Context, workspaceId, flowId string) ([]domain.Subflow, error) {
	if workspaceId == "" || flowId == "" {
		return nil, errors.New("workspaceId and flowId cannot be empty")
	}

	query := `
		SELECT id, workspace_id, name, description, status, parent_subflow_id, flow_id, result
		FROM subflows
		WHERE workspace_id = ? AND flow_id = ?
	`

	rows, err := s.db.QueryContext(ctx, query, workspaceId, flowId)
	if err != nil {
		log.Error().Err(err).
			Str("workspaceId", workspaceId).
			Str("flowId", flowId).
			Msg("Failed to query subflows")
		return nil, fmt.Errorf("failed to query subflows: %w", err)
	}
	defer rows.Close()

	var subflows []domain.Subflow
	for rows.Next() {
		var subflow domain.Subflow
		var result []byte

		err := rows.Scan(
			&subflow.Id,
			&subflow.WorkspaceId,
			&subflow.Name,
			&subflow.Description,
			&subflow.Status,
			&subflow.ParentSubflowId,
			&subflow.FlowId,
			&result,
		)
		if err != nil {
			log.Error().Err(err).
				Str("workspaceId", workspaceId).
				Str("flowId", flowId).
				Msg("Failed to scan subflow row")
			return nil, fmt.Errorf("failed to scan subflow row: %w", err)
		}

		if len(result) > 0 {
			err = json.Unmarshal(result, &subflow.Result)
			if err != nil {
				log.Error().Err(err).
					Str("subflowId", subflow.Id).
					Msg("Failed to unmarshal subflow result")
				return nil, fmt.Errorf("failed to unmarshal subflow result: %w", err)
			}
		}

		subflows = append(subflows, subflow)
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).
			Str("workspaceId", workspaceId).
			Str("flowId", flowId).
			Msg("Error occurred while iterating over subflow rows")
		return nil, fmt.Errorf("error occurred while iterating over subflow rows: %w", err)
	}

	log.Debug().
		Str("workspaceId", workspaceId).
		Str("flowId", flowId).
		Int("count", len(subflows)).
		Msg("Subflows retrieved successfully")

	return subflows, nil
}
