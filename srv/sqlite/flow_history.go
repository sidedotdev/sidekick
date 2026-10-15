package sqlite

import (
	"context"

	"sidekick/domain"
)

var _ domain.FlowHistoryStorage = (*Storage)(nil)

func (s *Storage) GetFlowActionsPage(ctx context.Context, workspaceID, flowID string, page domain.HistoryPageRequest) (domain.FlowActionsPage, error) {
	cursor, err := page.Cursor(workspaceID, flowID, "actions")
	if err != nil {
		return domain.FlowActionsPage{}, err
	}
	query := `
		SELECT id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
		       created, updated, action_type, action_params, action_status, action_result,
		       is_human_action, is_callback_action, temporal_activities
		FROM flow_actions
		WHERE workspace_id = ? AND flow_id = ?`
	args := []interface{}{workspaceID, flowID}
	if page.Before != "" {
		query += ` AND (created, id) < (?, ?)`
		args = append(args, cursor.Created.UTC(), cursor.ID)
	}
	query += ` ORDER BY created DESC, id DESC LIMIT ?`
	args = append(args, page.Limit+1)
	actions, err := s.queryFlowActions(ctx, query, args...)
	if err != nil {
		return domain.FlowActionsPage{}, err
	}
	result := domain.FlowActionsPage{FlowActions: actions}
	if len(actions) > page.Limit {
		result.FlowActions = actions[:page.Limit]
		last := result.FlowActions[page.Limit-1]
		cursor.ID, cursor.Created = last.Id, last.Created
		result.NextCursor = cursor.Encode()
	}
	if result.FlowActions == nil {
		result.FlowActions = []domain.FlowAction{}
	}
	return result, nil
}

func (s *Storage) GetSubflowsPage(ctx context.Context, workspaceID, flowID string, page domain.HistoryPageRequest) (domain.SubflowsPage, error) {
	cursor, err := page.Cursor(workspaceID, flowID, "subflows")
	if err != nil {
		return domain.SubflowsPage{}, err
	}
	query := `
		SELECT id, workspace_id, type, name, description, status, parent_subflow_id, flow_id, result, updated
		FROM subflows
		WHERE workspace_id = ? AND flow_id = ?`
	args := []interface{}{workspaceID, flowID}
	// Subflow IDs contain creation time; updated changes as a subflow runs.
	if page.Before != "" {
		query += ` AND id < ?`
		args = append(args, cursor.ID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, page.Limit+1)
	subflows, err := s.querySubflows(ctx, workspaceID, flowID, query, args...)
	if err != nil {
		return domain.SubflowsPage{}, err
	}
	result := domain.SubflowsPage{Subflows: subflows}
	if len(subflows) > page.Limit {
		result.Subflows = subflows[:page.Limit]
		cursor.ID = result.Subflows[page.Limit-1].Id
		result.NextCursor = cursor.Encode()
	}
	if result.Subflows == nil {
		result.Subflows = []domain.Subflow{}
	}
	return result, nil
}
