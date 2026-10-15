package srv

import (
	"context"
	"fmt"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryStreamerFlowActionReplay(t *testing.T) {
	t.Parallel()

	for _, startID := range []string{"", "0", "$"} {
		for _, backlogSize := range []int{0, 1, 128} {
			t.Run(fmt.Sprintf("start=%q/backlog=%d", startID, backlogSize), func(t *testing.T) {
				t.Parallel()

				streamer := NewMemoryStreamer()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				require.NoError(t, streamer.AddFlowActionChange(ctx, domain.FlowAction{
					Id: "other-workspace", WorkspaceId: "other", FlowId: "flow",
				}))
				require.NoError(t, streamer.AddFlowActionChange(ctx, domain.FlowAction{
					Id: "other-flow", WorkspaceId: "workspace", FlowId: "other",
				}))

				var expected []domain.FlowAction
				for i := 0; i < backlogSize; i++ {
					action := domain.FlowAction{
						Id:          fmt.Sprintf("action-%d", i),
						ActionType:  "test",
						WorkspaceId: "workspace",
						FlowId:      "flow",
					}
					require.NoError(t, streamer.AddFlowActionChange(ctx, action))
					if startID != "$" {
						expected = append(expected, action)
					}
				}

				actions, errs := streamer.StreamFlowActionChanges(ctx, "workspace", "flow", startID)
				liveAction := domain.FlowAction{
					Id: "live", ActionType: "test", WorkspaceId: "workspace", FlowId: "flow",
				}
				require.NoError(t, streamer.AddFlowActionChange(ctx, liveAction))
				expected = append(expected, liveAction)

				for _, want := range expected {
					select {
					case got, ok := <-actions:
						require.True(t, ok, "action stream closed early")
						require.Equal(t, want, got)
					case err := <-errs:
						t.Fatalf("unexpected stream error or closure: %v", err)
					case <-ctx.Done():
						t.Fatalf("waiting for action %q: %v", want.Id, ctx.Err())
					}
				}

				select {
				case extra := <-actions:
					t.Fatalf("unexpected extra action: %+v", extra)
				default:
				}
			})
		}
	}
}
