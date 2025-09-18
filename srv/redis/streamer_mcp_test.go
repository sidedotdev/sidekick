package redis

import (
	"context"
	"encoding/json"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddMCPToolCallEvent(t *testing.T) {
	streamer := NewTestRedisStreamer()
	ctx := context.Background()

	workspaceId := "ws1"
	sessionId := "sess1"
	streamKey := "mcp_session:tool_calls:ws1:sess1"

	event := domain.MCPToolCallEvent{
		ToolName:   "list_tasks",
		Status:     domain.MCPToolCallStatusPending,
		ArgsJSON:   `{"statuses":["to_do","in_progress"]}`,
		ResultJSON: "",
		Error:      "",
	}

	// Add the event
	err := streamer.AddMCPToolCallEvent(ctx, workspaceId, sessionId, event)
	require.NoError(t, err)

	// Read the event back from the stream
	result := streamer.Client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamKey, "0"},
		Count:   1,
	})
	require.NoError(t, result.Err())

	streams := result.Val()
	require.Len(t, streams, 1)
	require.Len(t, streams[0].Messages, 1)

	message := streams[0].Messages[0]
	eventData, exists := message.Values["event"]
	require.True(t, exists)

	// Unmarshal and verify the event
	var receivedEvent domain.MCPToolCallEvent
	err = json.Unmarshal([]byte(eventData.(string)), &receivedEvent)
	require.NoError(t, err)

	assert.Equal(t, event.ToolName, receivedEvent.ToolName)
	assert.Equal(t, event.Status, receivedEvent.Status)
	assert.Equal(t, event.ArgsJSON, receivedEvent.ArgsJSON)
	assert.Equal(t, event.ResultJSON, receivedEvent.ResultJSON)
	assert.Equal(t, event.Error, receivedEvent.Error)

	// Verify TTL is set to roughly 1 hour
	ttl := streamer.Client.TTL(ctx, streamKey)
	require.NoError(t, ttl.Err())

	ttlDuration := ttl.Val()
	assert.Greater(t, ttlDuration, time.Duration(0))
	assert.LessOrEqual(t, ttlDuration, time.Hour)
	// Should be close to 1 hour (allowing some margin for test execution time)
	assert.Greater(t, ttlDuration, time.Hour-time.Minute)
}
