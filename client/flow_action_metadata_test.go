package client

import (
	"encoding/json"
	"sidekick/domain"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlowActionMetadataJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		metadata string
	}{
		{name: "legacy"},
		{name: "arbitrary metadata and links", metadata: `{"custom":{"enabled":true,"labels":["a","b"]},"links":[{"rel":"approval_for","flowActionId":"fa_origin","toolCallId":"call_1"},{"rel":"related","toolCallId":"call_2"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload := `{"id":"fa_approval","flowId":"flow_1","workspaceId":"ws_1"`
			if tc.metadata != "" {
				payload += `,"metadata":` + tc.metadata
			}
			payload += `}`
			var action domain.FlowAction
			require.NoError(t, json.Unmarshal([]byte(payload), &action))
			serverJSON, err := json.Marshal(action)
			require.NoError(t, err)
			var clientAction FlowAction
			require.NoError(t, json.Unmarshal(serverJSON, &clientAction))
			clientJSON, err := json.Marshal(clientAction)
			require.NoError(t, err)
			for _, data := range [][]byte{serverJSON, clientJSON} {
				var decoded map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(data, &decoded))
				if tc.metadata == "" {
					require.NotContains(t, decoded, "metadata")
				} else {
					require.JSONEq(t, tc.metadata, string(decoded["metadata"]))
				}
			}
		})
	}
}
