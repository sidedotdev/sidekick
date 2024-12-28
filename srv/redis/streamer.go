package redis

import (
	"context"
	"sidekick/domain"

	"github.com/redis/go-redis/v9"
)

type Streamer struct {
	Client *redis.Client
}

func (s *Streamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	panic("StreamTaskChanges not implemented for Redis")
}

func NewStreamer() *Streamer {
	return &Streamer{Client: setupClient()}
}
