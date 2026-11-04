package redis

import (
	"context"
	"strings"
	"testing"

	"sidekick/domain"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisTestHelperIsolation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		client func(*testing.T) *redis.Client
	}{
		{
			name: "storage",
			client: func(t *testing.T) *redis.Client {
				return newTestRedisStorage(t).Client
			},
		},
		{
			name: "streamer",
			client: func(t *testing.T) *redis.Client {
				return newTestRedisStreamer(t).Client
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			first := newTestRedisStorage(t)
			action := domain.FlowAction{
				Id: "fa_shared", WorkspaceId: "ws_shared", FlowId: "flow_shared",
				ActionResult: "first",
			}
			require.NoError(t, first.PersistFlowAction(ctx, action))

			second := &Storage{Client: tc.client(t)}
			stored, err := first.GetFlowAction(ctx, action.WorkspaceId, action.Id)
			require.NoError(t, err)
			require.Equal(t, action, stored)

			other := action
			other.ActionResult = "second"
			require.NoError(t, second.PersistFlowAction(ctx, other))
			stored, err = first.GetFlowAction(ctx, action.WorkspaceId, action.Id)
			require.NoError(t, err)
			require.Equal(t, action, stored)
			stored, err = second.GetFlowAction(ctx, other.WorkspaceId, other.Id)
			require.NoError(t, err)
			require.Equal(t, other, stored)
		})
	}
}

func TestRedisTestHelperLongTestName(t *testing.T) {
	t.Parallel()
	t.Run(strings.Repeat("long-test-name-", 12), func(t *testing.T) {
		t.Parallel()
		client := newTestRedisClient(t)
		ctx, cancel := context.WithTimeout(context.Background(), client.Options().DialTimeout)
		defer cancel()
		require.NoError(t, client.Ping(ctx).Err())
	})
}
