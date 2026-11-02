package sqlite

import (
	"context"
	"encoding/json"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFlowActionMetadata(t *testing.T) {
	t.Parallel()
	storage := NewTestSqliteStorage(t, "flow_action_metadata")
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	ctx := context.Background()
	metadata := `{"links":[{"rel":"approval_for","flowActionId":"fa_other","toolCallId":"call_1"},{"rel":"related","flowActionId":"fa_elsewhere"}],"custom":{"enabled":true,"labels":["one","two"]}}`
	var action domain.FlowAction
	require.NoError(t, json.Unmarshal([]byte(`{"id":"fa_test","workspaceId":"ws_test","flowId":"flow_test","actionParams":{},"metadata":`+metadata+`}`), &action))
	action.Created = time.Now().UTC()
	action.Updated = action.Created

	for _, status := range []string{domain.ActionStatusPending, domain.ActionStatusComplete} {
		action.ActionStatus = status
		require.NoError(t, storage.PersistFlowAction(ctx, action))
		single, err := storage.GetFlowAction(ctx, action.WorkspaceId, action.Id)
		require.NoError(t, err)
		list, err := storage.GetFlowActions(ctx, action.WorkspaceId, action.FlowId)
		require.NoError(t, err)
		require.Len(t, list, 1)
		page, err := storage.GetFlowActionsPage(ctx, action.WorkspaceId, action.FlowId, domain.HistoryPageRequest{Limit: 10})
		require.NoError(t, err)
		require.Len(t, page.FlowActions, 1)
		for _, value := range []domain.FlowAction{single, list[0], page.FlowActions[0]} {
			data, err := json.Marshal(value)
			require.NoError(t, err)
			var decoded map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.JSONEq(t, metadata, string(decoded["metadata"]))
		}
	}

	_, err := storage.db.ExecContext(ctx, `UPDATE flow_actions SET metadata = NULL WHERE id = ?`, action.Id)
	require.NoError(t, err)
	legacy, err := storage.GetFlowAction(ctx, action.WorkspaceId, action.Id)
	require.NoError(t, err)
	data, err := json.Marshal(legacy)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotContains(t, decoded, "metadata")
}
