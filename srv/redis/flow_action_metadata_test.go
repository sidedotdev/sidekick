package redis

import (
	"context"
	"encoding/json"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
)

func TestFlowActionChangesMetadata(t *testing.T) {
	streamer := newTestRedisStreamer(t)
	defer streamer.Client.Close()
	ctx := context.Background()
	metadata := `{"links":[{"rel":"approval_for","flowActionId":"fa_origin","toolCallId":"call_1"},{"rel":"related","toolCallId":"call_2"}],"custom":{"enabled":true,"labels":["a","b"]}}`
	var action domain.FlowAction
	require.NoError(t, json.Unmarshal([]byte(`{"id":"fa_metadata","workspaceId":"ws_test","actionParams":{},"metadata":`+metadata+`}`), &action))
	action.FlowId = "flow_" + ksuid.New().String()
	action.Created = time.Now().UTC()
	action.Updated = action.Created

	for _, status := range []string{domain.ActionStatusPending, domain.ActionStatusComplete} {
		action.ActionStatus = status
		require.NoError(t, streamer.AddFlowActionChange(ctx, action))
	}
	legacy := domain.FlowAction{
		Id: "fa_legacy", WorkspaceId: action.WorkspaceId, FlowId: action.FlowId,
		Created: action.Created, Updated: action.Updated, ActionParams: map[string]any{},
	}
	require.NoError(t, streamer.AddFlowActionChange(ctx, legacy))
	changes, _, err := streamer.GetFlowActionChanges(ctx, action.WorkspaceId, action.FlowId, "0", 10, 0)
	require.NoError(t, err)
	require.Len(t, changes, 3)
	for i, change := range changes {
		data, err := json.Marshal(change)
		require.NoError(t, err)
		var decoded map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &decoded))
		if i == 2 {
			require.NotContains(t, decoded, "metadata")
		} else {
			require.JSONEq(t, metadata, string(decoded["metadata"]))
		}
	}
}
