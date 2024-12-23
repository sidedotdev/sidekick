package redis

import (
	"github.com/redis/go-redis/v9"
)

type Streamer struct {
	Client *redis.Client
}

func NewStreamer() *Streamer {
	return &Streamer{Client: setupClient()}
}
