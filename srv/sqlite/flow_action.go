package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
)

// PersistFlowAction inserts or updates a FlowAction in the SQLite database
func (s *Storage) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	actionParamsJSON, err := json.Marshal(flowAction.ActionParams)
	if err != nil {
		return fmt.Errorf("failed to marshal ActionParams: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO flow_actions (
			id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
			created, updated, action_type, action_params, action_status, action_result,
			is_human_action, is_callback_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	flowAction.Created = flowAction.Created.UTC()
	flowAction.Updated = flowAction.Updated.UTC()

	_, err = s.db.ExecContext(ctx, query,
		flowAction.Id, flowAction.SubflowName, flowAction.SubflowDescription, flowAction.SubflowId,
		flowAction.FlowId, flowAction.WorkspaceId, flowAction.Created, flowAction.Updated,
		flowAction.ActionType, actionParamsJSON, flowAction.ActionStatus, flowAction.ActionResult,
		flowAction.IsHumanAction, flowAction.IsCallbackAction,
	)

	if err != nil {
		return fmt.Errorf("failed to persist flow action: %w", err)
	}

	return nil
}

// GetFlowActions retrieves multiple FlowActions from the SQLite database
func (s *Storage) GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]domain.FlowAction, error) {
	query := `
		SELECT id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
			   created, updated, action_type, action_params, action_status, action_result,
			   is_human_action, is_callback_action
		FROM flow_actions
		WHERE workspace_id = ? AND flow_id = ?
	`

	rows, err := s.db.QueryContext(ctx, query, workspaceId, flowId)
	if err != nil {
		return nil, fmt.Errorf("failed to query flow actions: %w", err)
	}
	defer rows.Close()

	var flowActions []domain.FlowAction
	for rows.Next() {
		var fa domain.FlowAction
		var actionParamsJSON []byte

		err := rows.Scan(
			&fa.Id, &fa.SubflowName, &fa.SubflowDescription, &fa.SubflowId,
			&fa.FlowId, &fa.WorkspaceId, &fa.Created, &fa.Updated,
			&fa.ActionType, &actionParamsJSON, &fa.ActionStatus, &fa.ActionResult,
			&fa.IsHumanAction, &fa.IsCallbackAction,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan flow action row: %w", err)
		}

		err = json.Unmarshal(actionParamsJSON, &fa.ActionParams)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal action params: %w", err)
		}

		flowActions = append(flowActions, fa)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over flow action rows: %w", err)
	}

	return flowActions, nil
}

// GetFlowAction retrieves a single FlowAction from the SQLite database
func (s *Storage) GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (domain.FlowAction, error) {
	query := `
		SELECT id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
			   created, updated, action_type, action_params, action_status, action_result,
			   is_human_action, is_callback_action
		FROM flow_actions
		WHERE workspace_id = ? AND id = ?
	`

	var fa domain.FlowAction
	var actionParamsJSON []byte

	err := s.db.QueryRowContext(ctx, query, workspaceId, flowActionId).Scan(
		&fa.Id, &fa.SubflowName, &fa.SubflowDescription, &fa.SubflowId,
		&fa.FlowId, &fa.WorkspaceId, &fa.Created, &fa.Updated,
		&fa.ActionType, &actionParamsJSON, &fa.ActionStatus, &fa.ActionResult,
		&fa.IsHumanAction, &fa.IsCallbackAction,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.FlowAction{}, common.ErrNotFound
		}
		return domain.FlowAction{}, fmt.Errorf("failed to get flow action: %w", err)
	}

	err = json.Unmarshal(actionParamsJSON, &fa.ActionParams)
	if err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to unmarshal action params: %w", err)
	}

	return fa, nil
}

func (s *Storage) DeleteFlowActionsForFlow(ctx context.Context, workspaceId, flowId string) error {
	query := "DELETE FROM flow_actions WHERE workspace_id = ? AND flow_id = ?"
	_, err := s.db.ExecContext(ctx, query, workspaceId, flowId)
	if err != nil {
		return fmt.Errorf("failed to delete flow actions for flow %s: %w", flowId, err)
	}
	return nil
}
