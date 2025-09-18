package redis

import (
	"context"
	"sidekick/domain"

	"github.com/redis/go-redis/v9"
)

type Streamer struct {
	Client *redis.Client
}

func NewStreamer() *Streamer {
	return &Streamer{Client: setupClient()}
}

func (s *Streamer) AddMCPToolCallEvent(ctx context.Context, workspaceId, sessionId string, event domain.MCPToolCallEvent) error {
	// TODO: Implement in step 3 of the plan
	return nil
}
