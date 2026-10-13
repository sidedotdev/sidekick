package srv

import (
	"context"
	"fmt"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryStreamerFlowEventReplay(t *testing.T) {
	t.Parallel()

	for _, backlogSize := range []int{0, 1, 128} {
		t.Run(fmt.Sprintf("backlog_%d", backlogSize), func(t *testing.T) {
			t.Parallel()

			streamer := NewMemoryStreamer()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			const workspaceID = "workspace"
			const flowID = "flow"
			const parentID = "parent"
			require.NoError(t, streamer.AddFlowEvent(ctx, workspaceID, flowID, domain.ProgressTextEvent{
				EventType: domain.ProgressTextEventType,
				ParentId:  "unsubscribed-parent",
				Text:      "unrelated history",
			}))
			want := make([]domain.FlowEvent, 0, backlogSize+1)
			for i := 0; i < backlogSize; i++ {
				event := domain.ProgressTextEvent{
					EventType: domain.ProgressTextEventType,
					ParentId:  parentID,
					Text:      fmt.Sprintf("stored event %d", i),
				}
				require.NoError(t, streamer.AddFlowEvent(ctx, workspaceID, flowID, event))
				want = append(want, event)
			}
			require.NoError(t, streamer.AddFlowEvent(ctx, workspaceID, "other-flow", domain.ProgressTextEvent{
				EventType: domain.ProgressTextEventType,
				ParentId:  parentID,
				Text:      "unrelated event",
			}))

			subscriptions := make(chan domain.FlowEventSubscription, 1)
			subscriptions <- domain.FlowEventSubscription{
				ParentId:             parentID,
				StreamMessageStartId: "0",
			}
			events, errs := streamer.StreamFlowEvents(ctx, workspaceID, flowID, subscriptions)
			require.NoError(t, streamer.AddFlowEvent(ctx, workspaceID, flowID, domain.ProgressTextEvent{
				EventType: domain.ProgressTextEventType,
				ParentId:  "unsubscribed-parent",
				Text:      "unrelated live event",
			}))

			live := domain.ProgressTextEvent{
				EventType: domain.ProgressTextEventType,
				ParentId:  parentID,
				Text:      "live event",
			}
			require.NoError(t, streamer.AddFlowEvent(ctx, workspaceID, flowID, live))
			want = append(want, live)

			for i, expected := range want {
				select {
				case event, ok := <-events:
					require.True(t, ok, "event stream closed at event %d", i)
					require.Equal(t, expected, event, "event %d", i)
				case err, ok := <-errs:
					t.Fatalf("unexpected stream error at event %d: %v (channel open: %t)", i, err, ok)
				case <-ctx.Done():
					t.Fatalf("timed out waiting for event %d: %v", i, ctx.Err())
				}
			}
		})
	}
}

func TestMemoryStreamerFlowEventSubscriptions(t *testing.T) {
	t.Parallel()

	streamer := NewMemoryStreamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subscriptions := make(chan domain.FlowEventSubscription)
	events, errs := streamer.StreamFlowEvents(ctx, "workspace", "flow", subscriptions)
	subscribe := func(parentID string) {
		t.Helper()
		select {
		case subscriptions <- domain.FlowEventSubscription{ParentId: parentID, StreamMessageStartId: "0"}:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	publish := func(parentID, text string) domain.FlowEvent {
		t.Helper()
		event := domain.ProgressTextEvent{
			EventType: domain.ProgressTextEventType,
			ParentId:  parentID,
			Text:      text,
		}
		require.NoError(t, streamer.AddFlowEvent(ctx, "workspace", "flow", event))
		return event
	}
	receive := func(expected domain.FlowEvent) {
		t.Helper()
		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.Equal(t, expected, event)
		case err, ok := <-errs:
			t.Fatalf("unexpected stream error: %v (channel open: %t)", err, ok)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	first := publish("first", "before subscription")
	second := publish("second", "before subscription")
	subscribe("first")
	receive(first)

	subscribe("first")
	secondLive := publish("second", "before second subscription")
	firstLive := publish("first", "after duplicate subscription")
	receive(firstLive)

	subscribe("second")
	receive(second)
	receive(secondLive)

	close(subscriptions)
	receive(publish("first", "after subscription channel closes"))
	cancel()

	select {
	case event, ok := <-events:
		require.False(t, ok, "unexpected extra event: %v", event)
	case <-time.After(5 * time.Second):
		t.Fatal("event channel did not close after cancellation")
	}
}

func TestMemoryStreamerFlowEventsLiveOnly(t *testing.T) {
	t.Parallel()

	streamer := NewMemoryStreamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, streamer.AddFlowEvent(ctx, "workspace", "flow", domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  "parent",
		Text:      "before subscription",
	}))

	subscriptions := make(chan domain.FlowEventSubscription)
	events, errs := streamer.StreamFlowEvents(ctx, "workspace", "flow", subscriptions)
	// Receiving the second subscription requires finishing registration of the first.
	for i := 0; i < 2; i++ {
		select {
		case subscriptions <- domain.FlowEventSubscription{ParentId: "parent", StreamMessageStartId: "$"}:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	live := domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  "parent",
		Text:      "after subscription",
	}
	require.NoError(t, streamer.AddFlowEvent(ctx, "workspace", "flow", live))
	select {
	case event, ok := <-events:
		require.True(t, ok)
		require.Equal(t, live, event)
	case err, ok := <-errs:
		t.Fatalf("unexpected stream error: %v (channel open: %t)", err, ok)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
