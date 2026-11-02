package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlowActionLinkJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		link FlowActionLink
		want string
	}{
		{
			name: "both identifiers",
			link: FlowActionLink{Rel: "approval_for", FlowActionId: "fa_origin", ToolCallId: "call_origin"},
			want: `{"rel":"approval_for","flowActionId":"fa_origin","toolCallId":"call_origin"}`,
		},
		{
			name: "only action",
			link: FlowActionLink{Rel: "related", FlowActionId: "fa_elsewhere"},
			want: `{"rel":"related","flowActionId":"fa_elsewhere"}`,
		},
		{
			name: "only tool call",
			link: FlowActionLink{Rel: "approval_for", ToolCallId: "call_origin"},
			want: `{"rel":"approval_for","toolCallId":"call_origin"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tc.link)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(data))
		})
	}
}
