package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"time"

	"github.com/redis/go-redis/v9"
)

type Streamer struct {
	Client *redis.Client
}

func NewStreamer() *Streamer {
	return &Streamer{Client: setupClient()}
}

func (s *Streamer) AddMCPToolCallEvent(ctx context.Context, workspaceId, sessionId string, event domain.MCPToolCallEvent) error {
	streamKey := fmt.Sprintf("mcp_session:tool_calls:%s:%s", workspaceId, sessionId)

	serializedEvent, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to serialize MCP tool call event: %v", err)
	}

	err = s.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]interface{}{"event": serializedEvent},
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to add MCP tool call event to stream: %v", err)
	}

	err = s.Client.Expire(ctx, streamKey, time.Hour).Err()
	if err != nil {
		return fmt.Errorf("failed to set TTL on MCP tool call event stream: %v", err)
	}

	return nil
}
