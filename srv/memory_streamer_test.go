package srv

import (
	"context"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStreamerStreamFlowActionChanges(t *testing.T) {
	t.Parallel()

	action := func(id, flowId string) domain.FlowAction {
		return domain.FlowAction{Id: id, WorkspaceId: "ws", FlowId: flowId}
	}

	cases := []struct {
		name                 string
		streamMessageStartId string
		before               []domain.FlowAction
		after                []domain.FlowAction
		wantIds              []string
	}{
		{
			name:                 "replays history from the start then streams live",
			streamMessageStartId: "0",
			before:               []domain.FlowAction{action("a1", "flow"), action("a2", "flow")},
			after:                []domain.FlowAction{action("a3", "flow")},
			wantIds:              []string{"a1", "a2", "a3"},
		},
		{
			name:                 "empty start id replays history",
			streamMessageStartId: "",
			before:               []domain.FlowAction{action("a1", "flow")},
			after:                []domain.FlowAction{action("a2", "flow")},
			wantIds:              []string{"a1", "a2"},
		},
		{
			name:                 "dollar start id streams only new actions",
			streamMessageStartId: "$",
			before:               []domain.FlowAction{action("a1", "flow")},
			after:                []domain.FlowAction{action("a2", "flow")},
			wantIds:              []string{"a2"},
		},
		{
			name:                 "other flows are neither replayed nor streamed",
			streamMessageStartId: "0",
			before:               []domain.FlowAction{action("other1", "other-flow"), action("a1", "flow")},
			after:                []domain.FlowAction{action("other2", "other-flow"), action("a2", "flow")},
			wantIds:              []string{"a1", "a2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := NewMemoryStreamer()
			for _, a := range tc.before {
				require.NoError(t, m.AddFlowActionChange(ctx, a))
			}

			actionCh, _ := m.StreamFlowActionChanges(ctx, "ws", "flow", tc.streamMessageStartId)
			for _, a := range tc.after {
				require.NoError(t, m.AddFlowActionChange(ctx, a))
			}

			var gotIds []string
			for range tc.wantIds {
				select {
				case a := <-actionCh:
					gotIds = append(gotIds, a.Id)
				case <-time.After(time.Second):
					t.Fatalf("timed out after receiving %v, want %v", gotIds, tc.wantIds)
				}
			}
			assert.Equal(t, tc.wantIds, gotIds)
			select {
			case a := <-actionCh:
				t.Fatalf("unexpected extra flow action %q", a.Id)
			default:
			}
		})
	}
}
