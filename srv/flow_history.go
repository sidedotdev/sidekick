package srv

import (
	"cmp"
	"context"
	"slices"

	"sidekick/domain"
)

func legacyFlowActionsPage(ctx context.Context, storage Storage, workspaceID, flowID string, page domain.HistoryPageRequest) (domain.FlowActionsPage, error) {
	cursor, err := page.Cursor(workspaceID, flowID, "actions")
	if err != nil {
		return domain.FlowActionsPage{}, err
	}
	actions, err := storage.GetFlowActions(ctx, workspaceID, flowID)
	if err != nil {
		return domain.FlowActionsPage{}, err
	}
	slices.SortFunc(actions, func(a, b domain.FlowAction) int {
		if order := b.Created.Compare(a.Created); order != 0 {
			return order
		}
		return cmp.Compare(b.Id, a.Id)
	})
	if page.Before != "" {
		actions = slices.DeleteFunc(actions, func(action domain.FlowAction) bool {
			return action.Created.After(cursor.Created) ||
				(action.Created.Equal(cursor.Created) && action.Id >= cursor.ID)
		})
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

func legacySubflowsPage(ctx context.Context, storage Storage, workspaceID, flowID string, page domain.HistoryPageRequest) (domain.SubflowsPage, error) {
	cursor, err := page.Cursor(workspaceID, flowID, "subflows")
	if err != nil {
		return domain.SubflowsPage{}, err
	}
	subflows, err := storage.GetSubflows(ctx, workspaceID, flowID)
	if err != nil {
		return domain.SubflowsPage{}, err
	}
	slices.SortFunc(subflows, func(a, b domain.Subflow) int {
		return cmp.Compare(b.Id, a.Id)
	})
	if page.Before != "" {
		subflows = slices.DeleteFunc(subflows, func(subflow domain.Subflow) bool {
			return subflow.Id >= cursor.ID
		})
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
