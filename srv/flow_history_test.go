package srv_test

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

func TestFlowHistoryStorageCompatibility(t *testing.T) {
	t.Parallel()
	storage := sqlite.NewTestSqliteStorage(t, "history_compatibility")
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	// Expose only the legacy storage interface while still using a real database.
	legacy := struct{ srv.Storage }{storage}
	service := srv.NewDelegator(legacy, nil)
	ctx := context.Background()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, storage.PersistFlowAction(ctx, domain.FlowAction{
			Id: id, FlowId: "flow", WorkspaceId: "ws", Created: created, Updated: created,
			ActionParams: map[string]interface{}{"id": id}, ActionResult: id,
		}))
		require.NoError(t, storage.PersistSubflow(ctx, domain.Subflow{
			Id: id, FlowId: "flow", WorkspaceId: "ws", Name: id,
		}))
	}
	actions, err := service.GetFlowActionsPage(ctx, "ws", "flow", domain.HistoryPageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, actions.FlowActions, 2)
	require.Equal(t, "c", actions.FlowActions[0].Id)
	olderActions, err := service.GetFlowActionsPage(ctx, "ws", "flow", domain.HistoryPageRequest{Limit: 2, Before: actions.NextCursor})
	require.NoError(t, err)
	require.Len(t, olderActions.FlowActions, 1)
	require.Equal(t, "a", olderActions.FlowActions[0].Id)
	require.Empty(t, olderActions.NextCursor)

	subflows, err := service.GetSubflowsPage(ctx, "ws", "flow", domain.HistoryPageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, subflows.Subflows, 2)
	require.Equal(t, "c", subflows.Subflows[0].Id)
	olderSubflows, err := service.GetSubflowsPage(ctx, "ws", "flow", domain.HistoryPageRequest{Limit: 2, Before: subflows.NextCursor})
	require.NoError(t, err)
	require.Len(t, olderSubflows.Subflows, 1)
	require.Equal(t, "a", olderSubflows.Subflows[0].Id)
	require.Empty(t, olderSubflows.NextCursor)
}
